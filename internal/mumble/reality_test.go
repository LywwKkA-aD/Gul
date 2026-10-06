package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/reality"
)

func TestSessionDialRealityDoesNotExposeProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Dial(DialConfig{Context: ctx, Address: testRealityAddress, Password: "private-password"},
		NewTOFUStore(t.TempDir(), testLogger(t)), testLogger(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("session lost setup cancellation")
	}
	for _, private := range []string{testRealityAddress, testRealityKey, "1234abcd", "cover.example.test", "voice.example.test", "private-password"} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("session error exposed private profile configuration")
		}
	}
}

func TestDialRealityPreservesMumbleTLSAndPinsTheServerHost(t *testing.T) {
	ep, err := parseEndpoint(testRealityAddress)
	if err != nil {
		t.Fatal(err)
	}
	certificate, _ := testServerCertificate(t, ep.host, 93)
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	finished := make(chan error, 1)
	go func() {
		server := tls.Server(serverSide, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		defer server.Close()
		if err := server.Handshake(); err != nil {
			finished <- err
			return
		}
		if got := server.ConnectionState().ServerName; got != ep.host {
			finished <- errors.New("inner TLS used camouflage name")
			return
		}
		_, err := io.Copy(server, server)
		finished <- err
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	calls := 0
	conn, err := dialRealityWith(ctx, DialConfig{Password: "join-password"}, ep, tofu,
		func(gotCtx context.Context, got reality.Config, target string) (net.Conn, error) {
			calls++
			if gotCtx != ctx || got.Server != "voice.example.test:443" || got.ServerName != "cover.example.test" ||
				got.PublicKey != testRealityKey || got.ShortID != "1234abcd" || got.Password != "join-password" || target != mumbleTarget {
				t.Fatal("REALITY dial used wrong route or configuration")
			}
			return clientSide, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if calls != 1 {
		t.Fatal("dial attempted more than one transport")
	}
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte("voice")); err != nil {
		t.Fatal(err)
	}
	var reply [5]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "voice" {
		t.Fatalf("TLS echo = %q, %v", reply, err)
	}
	other, _ := testServerCertificate(t, ep.host, 94)
	if err := tofu.TLSConfig(ep.host).VerifyPeerCertificate(other.Certificate, nil); !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("server host was not pinned: %v", err)
	}
	if err := tofu.TLSConfig(ep.realityServerName).VerifyPeerCertificate(other.Certificate, nil); err != nil {
		t.Fatalf("camouflage host incorrectly got the voice certificate pin: %v", err)
	}
	_ = conn.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("closing session did not release stream")
	}
}

func TestDialRealityRejectsWrongTransportAndRedactsErrors(t *testing.T) {
	ep, err := parseEndpoint(testRealityAddress)
	if err != nil {
		t.Fatal(err)
	}
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))
	called := false
	_, err = dialRealityWith(t.Context(), DialConfig{}, endpoint{kind: endpointHysteria}, tofu,
		func(context.Context, reality.Config, string) (net.Conn, error) {
			called = true
			return nil, nil
		})
	if err == nil || called {
		t.Fatal("wrong endpoint reached REALITY")
	}
	const password = "private-voice.example.test-password"
	cause := errors.New(password + " " + ep.address + " " + ep.realityPublicKey + " " + ep.realityServerName + " " + ep.realityShortID)
	_, err = dialRealityWith(t.Context(), DialConfig{Password: password}, ep, tofu,
		func(context.Context, reality.Config, string) (net.Conn, error) { return nil, cause })
	if !errors.Is(err, cause) {
		t.Fatal("dial lost error classification")
	}
	for _, private := range []string{password, ep.host, ep.realityPublicKey, ep.realityServerName, ep.realityShortID} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("dial error exposed connection configuration")
		}
	}
	if strings.Contains(err.Error(), "private-") {
		t.Fatal("address redaction left part of the password visible")
	}
}

func TestMumbleTLSRejectsNilStreams(t *testing.T) {
	var typedNil *net.TCPConn
	for _, conn := range []net.Conn{nil, typedNil} {
		if _, err := mumbleTLS(t.Context(), conn, DialConfig{}, endpoint{host: "voice.example.test"}, nil); err == nil {
			t.Fatal("nil stream was accepted")
		}
	}
}

func TestDialRealitySuppliesAnOptionalContext(t *testing.T) {
	ep, err := parseEndpoint(testRealityAddress)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dialRealityWith(nil, DialConfig{}, ep, nil, //nolint:staticcheck // Intentionally verifies the adapter's nil-context default.
		func(ctx context.Context, _ reality.Config, _ string) (net.Conn, error) {
			if ctx == nil {
				t.Fatal("optional session context remained nil")
			}
			return nil, context.Canceled
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDialRealityClosesPartialAndCanceledStreams(t *testing.T) {
	ep, err := parseEndpoint(testRealityAddress)
	if err != nil {
		t.Fatal(err)
	}
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))
	for _, withError := range []bool{true, false} {
		clientSide, serverSide := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := dialRealityWith(ctx, DialConfig{}, ep, tofu,
			func(context.Context, reality.Config, string) (net.Conn, error) {
				if withError {
					return clientSide, context.Canceled
				}
				return clientSide, nil
			})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled dial = %v", err)
		}
		_ = serverSide.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := serverSide.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Errorf("failed setup leaked stream: %v", err)
		}
		_ = serverSide.Close()
	}
}
