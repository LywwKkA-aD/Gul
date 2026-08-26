package mumble

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
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// startQUICEcho stands in for the relay: it reads the preamble and echoes
// whatever follows.
func startQUICEcho(t *testing.T) (address string, roots *tls.Config, seen <-chan relayproto.Credential) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
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

	socket, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	// The relay scrambles every datagram, so a stand-in for it has to as well
	// or the client is talking to something that cannot hear it.
	transport := &quic.Transport{
		Conn: relayproto.ObfuscatePacketConn(socket, relayproto.NewObfuscator(relayTestCredential())),
	}
	listener, err := transport.Listen(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{relayproto.QUICALPN},
	}, quicConfig())
	if err != nil {
		t.Fatalf("listen quic: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = transport.Close()
		_ = socket.Close()
	})

	credentials := make(chan relayproto.Credential, 1)
	go func() {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			return
		}
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		credential, err := relayproto.ReadQUICPreamble(stream)
		if err != nil {
			return
		}
		credentials <- credential
		_, _ = io.Copy(stream, stream)
	}()

	_, port, _ := net.SplitHostPort(socket.LocalAddr().String())
	return "wss://localhost:" + port, &tls.Config{RootCAs: pool}, credentials
}

// The QUIC road has to carry the tunnel and say who is calling before it does.
func TestDialQUICStatesTheCredentialAndCarriesTheStream(t *testing.T) {
	t.Parallel()
	address, roots, seen := startQUICEcho(t)
	want := relayTestCredential()

	stream, err := dialQUIC(t.Context(), address, want, roots)
	if err != nil {
		t.Fatalf("dial quic: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	select {
	case got := <-seen:
		if got != want {
			t.Fatalf("credential = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never saw a credential")
	}

	payload := []byte{0x16, 0x03, 0x03, 0x00, 0x02, 0xca, 0xfe}
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	echoed := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, echoed); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(echoed, payload) {
		t.Fatalf("echo = %x, want %x", echoed, payload)
	}
	if stream.RemoteAddr() == nil || stream.LocalAddr() == nil {
		t.Error("the stream has no addresses; it is not a usable net.Conn")
	}
}

// Without a password there is nothing to present, and that is worth saying
// before touching the network.
func TestDialQUICRefusesAnEmptyCredential(t *testing.T) {
	t.Parallel()
	if _, err := dialQUIC(t.Context(), "wss://localhost:1", "", nil); err != ErrRelayPasswordRequired {
		t.Fatalf("error = %v, want ErrRelayPasswordRequired", err)
	}
}

// The relay address carries no port for the ordinary case, and UDP has to
// land on the same number as HTTPS.
func TestQUICTargetDefaultsToTheHTTPSPort(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"wss://murmur.example.test":      "murmur.example.test:443",
		"wss://murmur.example.test:8443": "murmur.example.test:8443",
		"wss://[2001:db8::1]":            "[2001:db8::1]:443",
	}
	for address, want := range cases {
		got, _, err := quicTarget(address)
		if err != nil {
			t.Errorf("quicTarget(%q): %v", address, err)
			continue
		}
		if got != want {
			t.Errorf("quicTarget(%q) = %q, want %q", address, got, want)
		}
	}
	if _, _, err := quicTarget("://not a url"); err == nil {
		t.Error("an unparseable address was accepted")
	}
}
