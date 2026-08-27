//go:build live

package mumble

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relay"
	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// The whole client-to-relay-to-Murmur path, on both roads.
//
// Everything else tests one layer: the unit tests fake the transport, the
// other live tests reach Murmur directly on its own port, and the production
// checks probe the relay from outside without a client. Nothing exercised the
// join - a real client dialling a real relay in front of a real Murmur - and
// that join is where this milestone put all its risk: names derived from the
// password, a browser TLS handshake, a scrambled QUIC datagram, and a preamble
// the relay has to accept before Murmur ever hears a byte.
//
// Run with the stand up:
//
//	task murmur:up && go test -tags live ./internal/mumble -run TestClientReachesMurmurThroughTheRelay
const relayLiveSecret = "relay live test password"

// localRelay stands a relay up in front of the local Murmur stand and returns
// the address a client dials plus the roots that trust its outer certificate.
func localRelay(t *testing.T) (endpoint, *tls.Config, *tls.Config) {
	t.Helper()

	credential := relayproto.Derive([]byte(relayLiveSecret))
	handler, err := relay.NewHandler(relay.Config{
		// The client addresses the relay by IP, so that is the host the relay
		// must expect; anything else is answered as a wrong host.
		ExpectedHost:            "127.0.0.1",
		Upstream:                "127.0.0.1:64738",
		BearerCredentials:       []relayproto.Credential{credential},
		MaxConnections:          4,
		MaxConnectionsPerIP:     4,
		MaxWebSocketMessageSize: relayproto.MaxMessageBytes,
		Logger:                  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("relay handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", handler.Cover())
	for _, path := range handler.TunnelPaths() {
		mux.Handle(path, handler)
	}
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)

	// The outer roots: whatever httptest signed its listener with.
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	roots := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}

	// The same relay on UDP, with a certificate of its own for the same name.
	certificate, quicPool := relayLiveCertificate(t)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("listener address: %v", err)
	}
	quicServer, err := relay.ListenQUIC("127.0.0.1:"+port,
		func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &certificate, nil },
		handler, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("listen quic: %v", err)
	}
	go func() { _ = quicServer.Serve(t.Context()) }()
	t.Cleanup(func() { _ = quicServer.Close() })

	ep, err := parseEndpoint("wss://127.0.0.1:" + port)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	return ep, roots, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: quicPool}
}

// relayLiveCertificate signs a certificate for the loopback address, which is
// the name the QUIC client verifies.
func relayLiveCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
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

// A completed inner Mumble handshake is the proof: it can only finish if the
// derived path matched, the credential was accepted, the tunnel carried bytes
// both ways, and Murmur itself answered at the far end.
func TestClientReachesMurmurThroughTheRelay(t *testing.T) {
	ep, wssRoots, quicRoots := localRelay(t)
	credential := relayproto.Derive([]byte(relayLiveSecret))
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))

	t.Run("websocket", func(t *testing.T) {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: wssRoots}}
		conn, err := dialWSSMumbleTLS(t.Context(), ep, credential, tofu, nil, client)
		if err != nil {
			t.Fatalf("murmur was not reached over the websocket road: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
	})

	t.Run("quic", func(t *testing.T) {
		conn, err := dialQUICMumbleTLS(t.Context(), ep, credential, tofu, nil, quicRoots)
		if err != nil {
			t.Fatalf("murmur was not reached over the quic road: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
	})
}

// A client holding the wrong password reaches nothing on either road, and the
// relay says nothing about which part was wrong.
func TestRelayRefusesTheWrongPasswordOnBothRoads(t *testing.T) {
	ep, wssRoots, quicRoots := localRelay(t)
	wrong := relayproto.Derive([]byte("not the password"))
	tofu := NewTOFUStore(t.TempDir(), testLogger(t))

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: wssRoots}}
	if conn, err := dialWSSMumbleTLS(t.Context(), ep, wrong, tofu, nil, client); err == nil {
		_ = conn.Close()
		t.Error("the websocket road carried a wrong password through to Murmur")
	}
	if conn, err := dialQUICMumbleTLS(t.Context(), ep, wrong, tofu, nil, quicRoots); err == nil {
		_ = conn.Close()
		t.Error("the quic road carried a wrong password through to Murmur")
	}
}
