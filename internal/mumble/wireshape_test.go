package mumble

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// wireCellCeiling is the largest a single write may be once the tunnel is
// carrying traffic: one shaped cell, plus the WebSocket header and the outer
// TLS record overhead around it. Measured writes are 286 bytes; the slack is
// for a TLS version with a longer record header, not for a second cell.
const wireCellCeiling = 320

// The shaping is only worth its cost if the wire has one size on it. This
// watches the real client dial through the real contract and measures what an
// observer on the path would measure - not what any single layer intended.
//
// It exists because the intent and the wire disagreed. relayproto rounded every
// frame up to the grid, which hides a voice frame perfectly, and every test
// asked about voice. The inner TLS handshake is not voice: its records are 1300
// to 1500 bytes, they came out as frames of 1536 and 1280, and those were the
// only two sizes in an entire session that were not 256. Two users' uplinks
// stopped carrying anything at exactly 2856 bytes - those two frames and the
// packet after them - on both roads, on every build, for days.
//
// A layer-level test could not have caught it, because no layer was wrong on
// its own terms. Only the wire was.
func TestTheWireCarriesOneSizeFromTheFirstByte(t *testing.T) {
	t.Parallel()
	const host = "murmur.example.test"
	outerCertificate, outerRoots := testServerCertificate(t, host, 21)
	innerCertificate, _ := testServerCertificate(t, host, 22)

	messages := &sizeLog{}
	relayServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRelayRequest(w, r) {
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{relayTestNames().Tunnel},
		})
		if err != nil {
			return
		}
		ws.SetReadLimit(relayproto.MaxMessageBytes)
		stream := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
		defer func() { _ = stream.Close() }()
		// The relay's own view: it reads whole messages, so their sizes are
		// what its byte counter reports and what the journal shows.
		observed := relayproto.AsMessageConn(&observedConn{Conn: stream, reads: messages})
		shaped := relayproto.Shape(observed)
		if err := serveTestTunnel(shaped, innerCertificate.Certificate[0]); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, shaped)
	}))
	relayServer.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{outerCertificate},
	}
	relayServer.StartTLS()
	t.Cleanup(relayServer.Close)

	wire := &sizeLog{}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: outerRoots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			raw, err := (&net.Dialer{}).DialContext(ctx, network, relayServer.Listener.Addr().String())
			if err != nil {
				return nil, err
			}
			return &observedConn{Conn: raw, writes: wire}, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	port := strings.TrimPrefix(relayServer.URL, "https://127.0.0.1:")
	ep, err := parseEndpoint("wss://" + host + ":" + port)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	conn, err := dialWSSTunnel(t.Context(), ep, relayTestCredential(),
		NewTOFUStore(t.TempDir(), testLogger(t)), &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// The two packets that follow the handshake, then voice at the sizes a
	// variable-bitrate encoder produces.
	packets := newPacketConn(conn)
	for _, p := range [][]byte{mumblePacket(0, make([]byte, 26)), mumblePacket(2, make([]byte, 48))} {
		if _, err := packets.Write(p); err != nil {
			t.Fatalf("control packet: %v", err)
		}
	}
	for i := range 12 {
		if _, err := packets.Write(mumblePacket(1, make([]byte, 34+i*5))); err != nil {
			t.Fatalf("voice %d: %v", i, err)
		}
	}

	// Every message the relay read is one cell. This is the byte counter in
	// the relay's journal, and it must show one number and no other.
	sizes := messages.sizes()
	if len(sizes) == 0 {
		t.Fatal("the relay read nothing")
	}
	for i, size := range sizes {
		if size != relayproto.ShapedCellBytes {
			t.Fatalf("message %d was %d bytes, want the %d byte cell; sizes seen: %v",
				i, size, relayproto.ShapedCellBytes, distinct(sizes))
		}
	}

	// And on the wire itself, where the outer TLS records are, nothing sticks
	// out either. The handshake bytes are skipped: the outer ClientHello is a
	// browser's and is meant to look like one.
	tail := wire.sizes()
	if len(tail) < len(sizes) {
		t.Fatalf("the wire carried %d writes for %d messages", len(tail), len(sizes))
	}
	for i, size := range tail[len(tail)-len(sizes):] {
		if size > wireCellCeiling {
			t.Fatalf("write %d put %d bytes on the wire, over the %d byte ceiling; "+
				"a size that stands out is the whole signature. writes: %v",
				i, size, wireCellCeiling, tail)
		}
	}
}

// sizeLog remembers the size of everything that passed through, which is all an
// observer of an encrypted stream gets.
type sizeLog struct {
	mu  sync.Mutex
	log []int
}

func (l *sizeLog) add(n int) {
	l.mu.Lock()
	l.log = append(l.log, n)
	l.mu.Unlock()
}

func (l *sizeLog) sizes() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.log)
}

// observedConn records sizes in whichever direction it was given a log for.
type observedConn struct {
	net.Conn
	writes *sizeLog
	reads  *sizeLog
}

func (c *observedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.writes != nil && n > 0 {
		c.writes.add(n)
	}
	return n, err
}

func (c *observedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.reads != nil && n > 0 {
		c.reads.add(n)
	}
	return n, err
}

func distinct(sizes []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, size := range sizes {
		if !seen[size] {
			seen[size] = true
			out = append(out, size)
		}
	}
	return out
}

// serveTestTunnel is the relay's half of the exchange, for tests that need a
// tunnel to exist rather than to test the relay.
func serveTestTunnel(stream net.Conn, leaf []byte) error {
	if _, err := relayproto.ReadTunnelHello(stream); err != nil {
		return err
	}
	if err := relayproto.WriteTunnelAccept(stream, relayproto.TunnelAccept{
		Version:     relayproto.TunnelVersion,
		Status:      relayproto.TunnelAccepted,
		Fingerprint: leaf,
	}); err != nil {
		return err
	}
	return relayproto.WriteTunnelReady(stream)
}

// testServerCertificate issues a self-signed leaf and the pool that trusts it.
func testServerCertificate(t *testing.T, host string, serial int64) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse certificate pair: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("append test root")
	}
	return certificate, roots
}
