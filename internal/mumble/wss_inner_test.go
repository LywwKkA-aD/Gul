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
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

func TestDialWSSMumbleTLSVerifiesOuterCAAndPinsInnerCertificate(t *testing.T) {
	const host = "murmur.example.test"
	const secret = "server password"
	outerCertificate, outerRoots := testServerCertificate(t, host, 1)
	innerCertificate, _ := testServerCertificate(t, host, 2)
	innerSNI := make(chan string, 1)

	relayServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := relayproto.ParseHeader(r.Header.Get("Authorization")); !ok || !c.Matches(relayproto.DeriveLegacy([]byte(secret))) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{relayproto.Subprotocol},
		})
		if err != nil {
			return
		}
		stream := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
		defer func() { _ = stream.Close() }()
		inner := tls.Server(stream, &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{innerCertificate},
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				innerSNI <- hello.ServerName
				return nil, nil
			},
		})
		defer func() { _ = inner.Close() }()
		if err := inner.HandshakeContext(r.Context()); err != nil {
			return
		}
		_, _ = io.Copy(inner, inner)
	}))
	relayServer.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{outerCertificate},
	}
	relayServer.StartTLS()
	t.Cleanup(relayServer.Close)

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: outerRoots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, relayServer.Listener.Addr().String())
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Transport: transport}
	port := strings.TrimPrefix(relayServer.URL, "https://127.0.0.1:")
	ep, err := parseEndpoint("wss://" + host + ":" + port + relayproto.Path)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	tofu, err := NewTOFUStore(t.TempDir())
	if err != nil {
		t.Fatalf("new TOFU store: %v", err)
	}

	conn, err := dialWSSMumbleTLS(t.Context(), ep, secret, tofu, nil, httpClient)
	if err != nil {
		t.Fatalf("dial nested TLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	select {
	case got := <-innerSNI:
		if got != host {
			t.Fatalf("inner TLS SNI = %q, want %q", got, host)
		}
	case <-time.After(time.Second):
		t.Fatal("inner TLS handshake did not report SNI")
	}
	if _, ok := tofu.Fingerprint(host); !ok {
		t.Fatal("inner Mumble certificate was not pinned")
	}

	want := []byte("opaque Mumble bytes")
	if _, err := conn.Write(want); err != nil {
		t.Fatalf("write nested stream: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read nested stream: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("nested stream = %q, want %q", got, want)
	}
}

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
