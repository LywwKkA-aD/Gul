package relay

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/identity"
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

// sayHello opens the exchange without waiting for the answer, for a test that
// cares about what the relay does next rather than about what it says back.
// Nothing happens on the relay before the hello arrives: the upstream is
// dialled after it, on purpose, so the answer can name which side is at fault.
func sayHello(t *testing.T, stream io.Writer) {
	t.Helper()
	if err := relayproto.WriteTunnelHello(stream, relayproto.TunnelHello{
		Version: relayproto.TunnelVersion,
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
}

// completeTunnel runs the exchange and returns the session, so a test that
// cares about what the session carries does not repeat the handshake.
func completeTunnel(t *testing.T, tunnel deadlineStream) {
	t.Helper()
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
	if accept.Status != relayproto.TunnelAccepted {
		t.Fatalf("status = %v, want accepted", accept.Status)
	}
	if err := relayproto.ReadTunnelReady(tunnel); err != nil {
		t.Fatalf("read ready: %v", err)
	}
	_ = tunnel.SetReadDeadline(time.Time{})
}

// deadlineStream is what the exchange needs of a connection, which is less
// than net.Conn: a QUIC stream has no addresses to report.
type deadlineStream interface {
	io.Reader
	io.Writer
	SetReadDeadline(time.Time) error
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
			Subprotocols: []string{names.Tunnel},
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
	// The attestation: this is the value the client would have computed itself
	// when the inner TLS was its own, so an existing pin keeps matching.
	wantFingerprint := sha256.Sum256(leaf)
	if got := string(accept.Fingerprint); got != hex.EncodeToString(wantFingerprint[:]) {
		t.Fatalf("the relay named %q, want the server's own %x", got, wantFingerprint)
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

// The identity the client derived is the identity Murmur is shown.
//
// This is what makes the user's name a fact the client can check rather than
// something the relay reports: the client works the fingerprint out before it
// connects, from a secret that never leaves the machine, and the relay rebuilds
// the same certificate from the seed it was handed. If the two ever disagree,
// a user is one person to their own client and another to the server, and
// nothing says so.
func TestTheTunnelPresentsTheIdentityTheClientDerived(t *testing.T) {
	t.Parallel()
	seen := make(chan []byte, 1)
	upstream, _ := tlsUpstreamWithClientAuth(t, seen)

	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = upstream
	cfg.UpstreamName = "murmur.example.test"
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	master := bytes.Repeat([]byte{0x5a}, identity.SeedBytes)
	hostSeed, err := identity.HostSeed(master, "murmur.example.test")
	if err != nil {
		t.Fatalf("host seed: %v", err)
	}
	want, err := identity.FromHostSeed(hostSeed)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	tunnel := dialTunnelRoad(t, server, defaultTestSecret)
	if err := relayproto.WriteTunnelHello(tunnel, relayproto.TunnelHello{
		Version:  relayproto.TunnelVersion,
		Identity: append([]byte{relayproto.IdentityEd25519Seed}, hostSeed...),
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
	accept, err := relayproto.ReadTunnelAccept(tunnel)
	if err != nil {
		t.Fatalf("read accept: %v", err)
	}
	if accept.Status != relayproto.TunnelAccepted {
		t.Fatalf("status = %v, want accepted", accept.Status)
	}

	select {
	case der := <-seen:
		sum := sha1.Sum(der)
		if got := hex.EncodeToString(sum[:]); got != want.Fingerprint {
			t.Fatalf("the server was shown %s, the client expects to be %s", got, want.Fingerprint)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server was shown no client certificate at all; the session is anonymous")
	}
}

// A secret the relay cannot use is refused, not quietly dropped.
//
// Opening the session anonymously instead would be the worse failure: a user
// who expects to be somebody would be made nobody without being told, lose
// whatever that name had on the server, and find out from the server rather
// than from us.
func TestAnIdentityTheRelayCannotUseIsRefused(t *testing.T) {
	t.Parallel()
	for name, offered := range map[string][]byte{
		"a kind nobody knows": append([]byte{0x7f}, bytes.Repeat([]byte{1}, identity.SeedBytes)...),
		"a seed of the wrong size": append([]byte{relayproto.IdentityEd25519Seed},
			bytes.Repeat([]byte{1}, identity.SeedBytes-3)...),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			upstream, _ := tlsUpstream(t, func(conn net.Conn) { _, _ = io.Copy(conn, conn) })
			cfg := baseConfig(defaultTestSecret)
			cfg.Upstream = upstream
			cfg.UpstreamName = "murmur.example.test"
			server := httptest.NewServer(mustHandler(t, cfg))
			t.Cleanup(server.Close)

			tunnel := dialTunnelRoad(t, server, defaultTestSecret)
			if err := relayproto.WriteTunnelHello(tunnel, relayproto.TunnelHello{
				Version:  relayproto.TunnelVersion,
				Identity: offered,
			}); err != nil {
				t.Fatalf("write hello: %v", err)
			}
			_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
			accept, err := relayproto.ReadTunnelAccept(tunnel)
			if err != nil {
				t.Fatalf("read accept: %v", err)
			}
			if accept.Status != relayproto.TunnelIdentityRefused {
				t.Fatalf("status = %v, want the identity refused rather than dropped", accept.Status)
			}
		})
	}
}

// tlsUpstreamWithClientAuth stands in for Murmur and reports the client
// certificate it was shown, which is the only way to check what the relay
// actually presented rather than what it said it would.
func tlsUpstreamWithClientAuth(t *testing.T, seen chan<- []byte) (string, []byte) {
	t.Helper()
	certificate, _ := quicTestCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequestClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) > 0 {
				select {
				case seen <- raw[0]:
				default:
				}
			}
			return nil
		},
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
			go func() { _, _ = io.Copy(conn, conn) }()
		}
	}()
	return listener.Addr().String(), certificate.Certificate[0]
}
