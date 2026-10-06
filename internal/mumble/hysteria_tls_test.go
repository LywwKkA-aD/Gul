package mumble

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/identity"
)

func TestMumbleTLSKeepsTheExistingIdentityAndPinsTheBackend(t *testing.T) {
	const host = "voice.example.test"
	certificate, _ := testServerCertificate(t, host, 91)
	seed := make([]byte, identity.SeedBytes)
	want, err := identity.ForHost(seed, host)
	if err != nil {
		t.Fatal(err)
	}
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))
	ep := endpoint{host: host}
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	gotIdentity := make(chan string, 1)
	go func() {
		server := tls.Server(serverSide, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			ClientAuth:   tls.RequireAnyClientCert,
			MinVersion:   tls.VersionTLS12,
		})
		defer server.Close()
		if err := server.Handshake(); err != nil {
			gotIdentity <- ""
			return
		}
		sum := sha1.Sum(server.ConnectionState().PeerCertificates[0].Raw)
		gotIdentity <- hex.EncodeToString(sum[:])
		_, _ = io.Copy(server, server)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conn, err := mumbleTLS(ctx, clientSide, DialConfig{IdentitySeed: seed}, ep, tofu)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := <-gotIdentity; got != want.Fingerprint {
		t.Fatalf("client identity = %q, want existing identity %q", got, want.Fingerprint)
	}
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("voice")); err != nil {
		t.Fatal(err)
	}
	var reply [5]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "voice" {
		t.Fatalf("TLS echo = %q, %v", reply, err)
	}
	other, _ := testServerCertificate(t, host, 92)
	if err := tofu.TLSConfig(host).VerifyPeerCertificate(other.Certificate, nil); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("backend certificate was not pinned: %v", err)
	}
}

func TestMumbleTLSCancellationClosesTheTunnel(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() {
		_, err := mumbleTLS(ctx, clientSide, DialConfig{}, endpoint{host: "voice.example.test"}, NewTOFUStore(t.TempDir(), testLogger(t)))
		finished <- err
	}()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled TLS returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TLS setup ignored cancellation")
	}
	_ = serverSide.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := serverSide.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("canceled tunnel still open: %v", err)
	}
}

func TestManagerDisconnectCancelsAnInflightDial(t *testing.T) {
	m := newTestManager(t, Callbacks{})
	started := make(chan struct{})
	m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
		close(started)
		if cfg.Context == nil {
			return nil, errors.New("dial has no cancellation context")
		}
		<-cfg.Context.Done()
		return nil, cfg.Context.Err()
	}
	m.Connect("voice.example.test", "gul", "test-password")
	<-started
	finished := make(chan struct{})
	go func() { m.Disconnect(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not interrupt dialing")
	}
}
