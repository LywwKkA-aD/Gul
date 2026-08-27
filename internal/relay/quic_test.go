package relay

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// quicTestCertificate returns a certificate and the pool that trusts it.
func quicTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: testHost},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{testHost},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// startQUICRelay brings up a QUIC relay in front of an echo upstream.
func startQUICRelay(t *testing.T, cfg Config) (*QUICServer, *x509.CertPool) {
	t.Helper()
	certificate, pool := quicTestCertificate(t)
	cfg.Upstream = echoServer(t)
	handler := mustHandler(t, cfg)
	server, err := ListenQUIC("127.0.0.1:0",
		func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &certificate, nil },
		handler, cfg.Logger)
	if err != nil {
		t.Fatalf("listen quic: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return server, pool
}

// dialQUICTunnel opens a tunnel the way the client does: scramble under the
// password, connect, open one stream, state the credential.
//
// scrambleWith is the password the datagrams are keyed with and present is
// what the preamble claims. They are the same for a real client; separating
// them is what lets a test reach the authorization check at all, because a
// caller who scrambles with the wrong password is not heard in the first place.
func dialQUICTunnel(
	t *testing.T,
	server *QUICServer,
	pool *x509.CertPool,
	scrambleWith, present relayproto.Credential,
) (net.Conn, error) {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Addr().String())
	if err != nil {
		t.Fatalf("listener address: %v", err)
	}
	remote, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	socket, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	transport := &quic.Transport{
		Conn: relayproto.ObfuscatePacketConn(socket, relayproto.NewObfuscator(scrambleWith)),
	}
	t.Cleanup(func() {
		_ = transport.Close()
		_ = socket.Close()
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, remote, &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: testHost,
		NextProtos: []string{relayproto.QUICALPN},
	}, quicConfig())
	if err != nil {
		return nil, err
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(0, "")
		return nil, err
	}
	if err := relayproto.WriteQUICPreamble(stream, present); err != nil {
		_ = conn.CloseWithError(0, "")
		return nil, err
	}
	t.Cleanup(func() { _ = conn.CloseWithError(0, "") })
	return &quicStreamConn{Stream: stream, conn: conn}, nil
}

// The second road has to carry the same bytes as the first.
func TestQUICRelayCarriesTheStream(t *testing.T) {
	t.Parallel()
	server, pool := startQUICRelay(t, baseConfig(defaultTestSecret))

	stream, err := dialQUICTunnel(t, server, pool, testCredential(defaultTestSecret), testCredential(defaultTestSecret))
	if err != nil {
		t.Fatalf("dial quic tunnel: %v", err)
	}

	want := []byte{0x16, 0x03, 0x03, 0x00, 0x05, 0xde, 0xad, 0xbe, 0xef}
	if _, err := stream.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(want))
	if err := stream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("echo = %x, want %x", got, want)
	}
}

// The wrong password is not refused - it is not heard. Datagrams scrambled
// under another key unscramble into noise, which QUIC discards, so the port
// never answers and there is nothing for a prober to learn from it.
func TestQUICRelayIsSilentToTheWrongPassword(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.Logger = slog.New(slog.DiscardHandler)
	server, pool := startQUICRelay(t, cfg)

	wrong := testCredential("some other password")
	if _, err := dialQUICTunnel(t, server, pool, wrong, wrong); err == nil {
		t.Fatal("a caller with the wrong password completed a handshake")
	}
}

// The preamble is still checked, so a caller who somehow gets its datagrams
// through without a credential the relay accepts is refused, and the refusal
// is logged for the operator.
func TestQUICRelayRefusesAnUnknownCredential(t *testing.T) {
	t.Parallel()
	logger, records := newRecordingLogger()
	cfg := baseConfig(defaultTestSecret)
	cfg.Logger = logger
	server, pool := startQUICRelay(t, cfg)

	stream, err := dialQUICTunnel(t, server, pool,
		testCredential(defaultTestSecret), testCredential("some other password"))
	if err != nil {
		records.await(t, "relay quic authorization rejected")
		return
	}
	_ = stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := stream.Read(make([]byte, 1)); err == nil {
		t.Fatalf("the tunnel carried %d bytes for an unknown credential", n)
	}
	records.await(t, "relay quic authorization rejected")
}

// A connection that opens and then says nothing must not hold anything open.
func TestQUICRelayRefusesASilentConnection(t *testing.T) {
	t.Parallel()
	logger, records := newRecordingLogger()
	cfg := baseConfig(defaultTestSecret)
	cfg.Logger = logger
	server, pool := startQUICRelay(t, cfg)

	_, port, _ := net.SplitHostPort(server.Addr().String())
	remote, _ := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", port))
	socket, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	transport := &quic.Transport{
		Conn: relayproto.ObfuscatePacketConn(socket, relayproto.NewObfuscator(testCredential(defaultTestSecret))),
	}
	t.Cleanup(func() {
		_ = transport.Close()
		_ = socket.Close()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, remote, &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: testHost,
		NextProtos: []string{relayproto.QUICALPN},
	}, quicConfig())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseWithError(0, "") })

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	// Open the stream, send nothing, and wait for the relay to lose interest.
	_ = stream.SetReadDeadline(time.Now().Add(quicPreambleTimeout + 5*time.Second))
	if _, err := io.ReadFull(stream, make([]byte, 1)); err == nil {
		t.Fatal("a connection that never identified itself was served")
	}
	// Either refusal is the right one, and which arrives depends on how far
	// the opener got: a QUIC stream with nothing written to it never reaches
	// the other side at all, so the relay gives up waiting for the stream
	// rather than for the preamble on it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, opened := records.find("relay quic stream never opened")
		_, rejected := records.find("relay quic preamble rejected")
		if opened || rejected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the refusal was not logged; recorded: %v", records.messages())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Close has to release the UDP socket, not just the listener and transport.
// quic.Transport.Close does not close a Conn it was handed, so without an
// explicit socket close the port stays bound - proven here by rebinding it,
// which fails while the fd leaks.
func TestQUICServerCloseReleasesThePort(t *testing.T) {
	t.Parallel()
	certificate, _ := quicTestCertificate(t)
	handler := mustHandler(t, baseConfig(defaultTestSecret))
	server, err := ListenQUIC("127.0.0.1:0",
		func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &certificate, nil },
		handler, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("listen quic: %v", err)
	}
	addr := server.Addr().String()
	if err := server.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The same address must bind again immediately; a leaked socket holds it.
	rebound, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("port still held after Close, socket leaked: %v", err)
	}
	_ = rebound.Close()
}
