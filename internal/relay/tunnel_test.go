package relay

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// tlsUpstream stands in for Murmur: a self-signed TLS server that echoes,
// which is what the relay now has to speak to instead of forwarding somebody
// else's TLS session to it. Returns its address and the DER of its leaf, which
// is the value the relay is supposed to hand the client to pin.
func tlsUpstream(t *testing.T, serve func(net.Conn)) (string, []byte) {
	t.Helper()
	certificate, _ := quicTestCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()
	return listener.Addr().String(), certificate.Certificate[0]
}

// dialTunnelRoad opens the tunnel contract the way a client will: the derived
// tunnel name, the cell grid under it, and the exchange on top.
func dialTunnelRoad(t *testing.T, server *httptest.Server, secret string) *relayproto.ShapedConn {
	t.Helper()
	names := relayproto.NamesFor(testCredential(secret))
	conn, response, err := websocket.Dial(t.Context(),
		"ws"+server.URL[len("http"):]+names.Path,
		&websocket.DialOptions{
			HTTPHeader:   bearerHeader(secret),
			Host:         testHost,
			Subprotocols: []string{names.Tunnel, names.Shaped},
		})
	if err != nil {
		t.Fatalf("dial tunnel: %v (%v)", err, response)
	}
	if got := conn.Subprotocol(); got != names.Tunnel {
		t.Fatalf("negotiated %q, want the tunnel contract", got)
	}
	conn.SetReadLimit(relayproto.MaxMessageBytes)
	t.Cleanup(func() { _ = conn.CloseNow() })
	stream := websocket.NetConn(t.Context(), conn, websocket.MessageBinary)
	return relayproto.Shape(relayproto.AsMessageConn(stream))
}

// The contract end to end: the client says hello, the relay opens Mumble TLS
// to the server itself, tells the client which certificate it found, and the
// bytes flow. No second TLS handshake crosses the network, which is the whole
// point of it.
func TestTheTunnelContractCarriesASessionAndNamesTheServer(t *testing.T) {
	t.Parallel()
	upstream, leaf := tlsUpstream(t, func(conn net.Conn) { _, _ = io.Copy(conn, conn) })

	logger, records := newRecordingLogger()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = upstream
	cfg.UpstreamName = "murmur.example.test"
	cfg.Logger = logger
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	tunnel := dialTunnelRoad(t, server, defaultTestSecret)

	if err := relayproto.WriteTunnelHello(tunnel, relayproto.TunnelHello{
		Version: relayproto.TunnelVersion,
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	accept, err := relayproto.ReadTunnelAccept(tunnel)
	if err != nil {
		t.Fatalf("read accept: %v", err)
	}
	if accept.Status != relayproto.TunnelAccepted {
		t.Fatalf("status = %v, want accepted", accept.Status)
	}
	// The attestation: this is the certificate the client would have pinned
	// itself when the inner TLS was its own.
	if !bytes.Equal(accept.Certificate, leaf) {
		t.Fatalf("the relay named a certificate that is not the server's:\n got %x\nwant %x",
			sha256.Sum256(accept.Certificate), sha256.Sum256(leaf))
	}
	if err := relayproto.ReadTunnelReady(tunnel); err != nil {
		t.Fatalf("read ready: %v", err)
	}

	// And the session itself carries Mumble, which the echo sends back.
	want := []byte("opaque Mumble bytes")
	if _, err := tunnel.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(want))
	_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(tunnel, got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("session carried %q, want %q", got, want)
	}

	attrs := recordAttrs(records.await(t, "relay session opened"))
	if attrs["contract"] != contractTunnel {
		t.Fatalf("logged contract = %q, want %q", attrs["contract"], contractTunnel)
	}
}

// A server that is not running is not a road that does not work, and the
// difference has to reach the client.
//
// Today it does not: the relay closes the WebSocket with an internal error the
// client never reads, so the client sees a road that opened and died and goes
// hunting through every other road it has. The tunnel says it in a frame.
func TestTheTunnelSaysWhenTheServerIsDownRatherThanJustClosing(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = "127.0.0.1:9" // discard: nothing listens
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	tunnel := dialTunnelRoad(t, server, defaultTestSecret)
	if err := relayproto.WriteTunnelHello(tunnel, relayproto.TunnelHello{
		Version: relayproto.TunnelVersion,
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
	accept, err := relayproto.ReadTunnelAccept(tunnel)
	if err != nil {
		t.Fatalf("read accept: %v", err)
	}
	if accept.Status != relayproto.TunnelUpstreamDown {
		t.Fatalf("status = %v, want the server named as the thing that is down", accept.Status)
	}
}

// The pin is the only thing standing where the certificate chain would be, so
// a leaf that does not match it must not carry a session - and the client must
// be told the server is the problem rather than left to guess.
func TestTheTunnelRefusesAnUpstreamThatDoesNotMatchThePin(t *testing.T) {
	t.Parallel()
	upstream, leaf := tlsUpstream(t, func(conn net.Conn) { _, _ = io.Copy(conn, conn) })
	wrong := sha256.Sum256(append([]byte("not this one"), leaf...))

	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = upstream
	cfg.UpstreamName = "murmur.example.test"
	cfg.UpstreamFingerprint = hex.EncodeToString(wrong[:])
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	tunnel := dialTunnelRoad(t, server, defaultTestSecret)
	if err := relayproto.WriteTunnelHello(tunnel, relayproto.TunnelHello{
		Version: relayproto.TunnelVersion,
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
	accept, err := relayproto.ReadTunnelAccept(tunnel)
	if err != nil {
		t.Fatalf("read accept: %v", err)
	}
	if accept.Status == relayproto.TunnelAccepted {
		t.Fatal("a session was carried to a server whose certificate did not match the pin")
	}
}

// An identity nobody has proved is not an identity. Until the exchange that
// proves possession exists, a certificate in the hello has to be ignored -
// honouring it would let the relay, or anyone who copied the bytes, be that
// user in front of Murmur.
func TestTheTunnelIgnoresAnUnprovenIdentity(t *testing.T) {
	t.Parallel()
	upstream, _ := tlsUpstream(t, func(conn net.Conn) { _, _ = io.Copy(conn, conn) })

	logger, records := newRecordingLogger()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = upstream
	cfg.UpstreamName = "murmur.example.test"
	cfg.Logger = logger
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	tunnel := dialTunnelRoad(t, server, defaultTestSecret)
	if err := relayproto.WriteTunnelHello(tunnel, relayproto.TunnelHello{
		Version:     relayproto.TunnelVersion,
		Certificate: bytes.Repeat([]byte{0x30, 0x82}, 200),
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := relayproto.ReadTunnelAccept(tunnel); err != nil {
		t.Fatalf("read accept: %v", err)
	}
	// The session opens - anonymously - and the journal says the claim was
	// seen and dropped, so nobody later mistakes silence for support.
	if rendered := records.rendered(); !strings.Contains(rendered, "unproven identity") {
		t.Fatalf("an identity claim passed without a word in the journal:\n%s", rendered)
	}
}
