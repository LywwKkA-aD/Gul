package hysteria

import (
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

	hyserver "github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/obfs"
)

const testPassword = "test-hysteria-password"

type passwordAuth string

func (p passwordAuth) Authenticate(_ net.Addr, password string, _ uint64) (bool, string) {
	return password == string(p), "test-client"
}

func startTestServer(t *testing.T, obfuscation string, outbound hyserver.Outbound) Config {
	t.Helper()
	cert, roots := testCertificate(t)
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = packet.Close() })
	transport := packet
	switch obfuscation {
	case "salamander":
		transport, err = obfs.WrapPacketConnSalamander(packet, []byte(testPassword))
	case "gecko":
		transport, err = obfs.WrapPacketConnGecko(packet, obfs.GeckoOptions{Password: []byte(testPassword)})
	}
	if err != nil {
		t.Fatal(err)
	}
	server, err := hyserver.NewServer(&hyserver.Config{
		Conn:          transport,
		TLSConfig:     hyserver.TLSConfig{Certificates: []tls.Certificate{cert}},
		Authenticator: passwordAuth(testPassword),
		Outbound:      outbound,
		DisableUDP:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve()
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Hysteria server did not stop")
		}
	})
	return Config{
		Server:      packet.LocalAddr().String(),
		ServerName:  "localhost",
		Password:    testPassword,
		Obfuscation: obfuscation,
		TLSConfig:   &tls.Config{RootCAs: roots},
	}
}

func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}, roots
}

func startEchoBackend(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = conn.Write([]byte("ready"))
		_, _ = io.Copy(conn, conn)
	}()
	return listener.Addr().String(), closed
}

type blockingOutbound struct {
	requested chan struct{}
	release   chan struct{}
}

func (b *blockingOutbound) TCP(string) (net.Conn, error) {
	close(b.requested)
	<-b.release
	return nil, net.ErrClosed
}

func (*blockingOutbound) UDP(string) (hyserver.UDPConn, error) { return nil, net.ErrClosed }
func (*blockingOutbound) CheckUDP(string) error                { return net.ErrClosed }
