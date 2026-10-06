package mumble

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
	"github.com/LywwKkA-aD/gumble/gumble/proto/MumbleProto"
	hyserver "github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/obfs"
	"google.golang.org/protobuf/proto"
)

const hysteriaFixturePassword = "integration-test-password"

type hysteriaFixture struct {
	endpoint     endpoint
	roots        *tls.Config
	requests     <-chan string
	disconnected <-chan struct{}
}

type fixtureEvents struct{ disconnected chan<- struct{} }

func (fixtureEvents) Connect(net.Addr, string, uint64)            {}
func (fixtureEvents) TCPRequest(net.Addr, string, string)         {}
func (fixtureEvents) TCPError(net.Addr, string, string, error)    {}
func (fixtureEvents) UDPRequest(net.Addr, string, uint32, string) {}
func (fixtureEvents) UDPError(net.Addr, string, uint32, error)    {}
func (e fixtureEvents) Disconnect(net.Addr, string, error) {
	select {
	case e.disconnected <- struct{}{}:
	default:
	}
}

type fixtureAuth []string

func (a fixtureAuth) Authenticate(_ net.Addr, password string, _ uint64) (bool, string) {
	return slices.Contains(a, password), "test-client"
}

type fixtureOutbound struct {
	target   string
	requests chan string
}

func (o *fixtureOutbound) TCP(target string) (net.Conn, error) {
	select {
	case o.requests <- target:
	default:
	}
	if target != "127.0.0.1:64738" {
		return nil, errors.New("fixture refuses an unexpected destination")
	}
	return net.DialTimeout("tcp", o.target, 3*time.Second)
}

func (*fixtureOutbound) UDP(string) (hyserver.UDPConn, error) { return nil, net.ErrClosed }
func (*fixtureOutbound) CheckUDP(string) error                { return net.ErrClosed }

// The official server forwards only Gul's fixed Mumble destination. Mapping
// that destination to an ephemeral listener keeps ordinary tests independent
// of the developer's Murmur installation and public network.
func startHysteriaFixture(t *testing.T, target, mode string, passwords []string) hysteriaFixture {
	t.Helper()
	cert, roots := hysteriaFixtureCertificate(t)
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = packet.Close() })
	transport := packet
	switch mode {
	case "salamander":
		transport, err = obfs.WrapPacketConnSalamander(packet, []byte(passwords[0]))
	case "gecko":
		transport, err = obfs.WrapPacketConnGecko(packet, obfs.GeckoOptions{Password: []byte(passwords[0])})
	}
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 8)
	disconnected := make(chan struct{}, 8)
	server, err := hyserver.NewServer(&hyserver.Config{
		Conn:          transport,
		TLSConfig:     hyserver.TLSConfig{Certificates: []tls.Certificate{cert}},
		Authenticator: fixtureAuth(passwords),
		Outbound:      &fixtureOutbound{target: target, requests: requests},
		DisableUDP:    true,
		EventLogger:   fixtureEvents{disconnected: disconnected},
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
			t.Error("Hysteria fixture did not stop")
		}
	})
	address := "hysteria2://" + packet.LocalAddr().String()
	if mode != "" {
		address += "?obfs=" + mode
	}
	ep, err := parseEndpoint(address)
	if err != nil {
		t.Fatal(err)
	}
	return hysteriaFixture{
		endpoint: ep, roots: &tls.Config{RootCAs: roots},
		requests: requests, disconnected: disconnected,
	}
}

func hysteriaFixtureCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Gul test server"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, roots
}

type fixtureLogin struct {
	username    string
	fingerprint string
	validAuth   bool
	opus        bool
}

type mumbleFixture struct {
	address     string
	certificate tls.Certificate
	logins      <-chan fixtureLogin
	closed      <-chan struct{}
	failures    <-chan error
	drop        context.CancelFunc
}

// This fixture speaks the small protocol subset needed to exercise Gul's
// complete connection: TLS identity, authentication, room sync, ping, text and
// tunneled Opus packets. It does not replace the live two-client Murmur tests.
func startMumbleFixture(t *testing.T, synchronize bool) mumbleFixture {
	t.Helper()
	certificate, _ := hysteriaFixtureCertificate(t)
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	logins := make(chan fixtureLogin, 1)
	closed := make(chan struct{})
	failures := make(chan error, 1)
	ctx, drop := context.WithCancel(t.Context())
	t.Cleanup(drop)
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		if err := serveMumbleFixture(conn.(*tls.Conn), synchronize, logins); err != nil {
			failures <- err
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Error("Mumble fixture did not stop")
		}
	})
	return mumbleFixture{
		address: listener.Addr().String(), certificate: certificate,
		logins: logins, closed: closed, failures: failures, drop: drop,
	}
}

func serveMumbleFixture(secured *tls.Conn, synchronize bool, logins chan<- fixtureLogin) error {
	if err := secured.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := secured.Handshake(); err != nil {
		return err
	}
	fingerprint := ""
	if peers := secured.ConnectionState().PeerCertificates; len(peers) > 0 {
		sum := sha1.Sum(peers[0].Raw)
		fingerprint = hex.EncodeToString(sum[:])
	}
	conn := gumble.NewConn(secured)
	conn.Timeout = 5 * time.Second
	for {
		kind, payload, err := conn.ReadPacket()
		if err != nil {
			return nil
		}
		switch kind {
		case 0: // Version
		case 2: // Authenticate
			var auth MumbleProto.Authenticate
			if err := proto.Unmarshal(payload, &auth); err != nil {
				return err
			}
			valid := auth.GetPassword() == hysteriaFixturePassword
			logins <- fixtureLogin{auth.GetUsername(), fingerprint, valid, auth.GetOpus()}
			if !valid {
				return errors.New("Mumble authentication did not receive the expected password")
			}
			if synchronize {
				if err := sendMumbleFixtureSync(conn, auth.GetUsername(), fingerprint); err != nil {
					return err
				}
			}
		case 3: // Ping
			if err := conn.WritePacket(kind, payload); err != nil {
				return err
			}
		case 11: // TextMessage
			var message MumbleProto.TextMessage
			if err := proto.Unmarshal(payload, &message); err != nil {
				return err
			}
			message.Actor = proto.Uint32(1)
			if err := conn.WriteProto(&message); err != nil {
				return err
			}
		case 1: // Legacy Opus tunnel: server adds the sender session varint.
			if len(payload) < 2 {
				return errors.New("incomplete voice packet")
			}
			incoming := append([]byte{payload[0], 1}, payload[1:]...)
			if err := conn.WritePacket(kind, incoming); err != nil {
				return err
			}
		case 8: // UserRemove on a deliberate disconnect.
			return nil
		default:
			return fmt.Errorf("unexpected Mumble packet type %d", kind)
		}
	}
}

func sendMumbleFixtureSync(conn *gumble.Conn, username, fingerprint string) error {
	for _, message := range []proto.Message{
		&MumbleProto.Version{VersionV1: proto.Uint32(0x010400)},
		&MumbleProto.ChannelState{ChannelId: proto.Uint32(0), Name: proto.String("Root")},
		&MumbleProto.UserState{
			Session: proto.Uint32(1), Name: proto.String(username),
			ChannelId: proto.Uint32(0), Hash: proto.String(fingerprint),
		},
		&MumbleProto.CodecVersion{
			Alpha: proto.Int32(0), Beta: proto.Int32(0),
			PreferAlpha: proto.Bool(false), Opus: proto.Bool(true),
		},
		&MumbleProto.ServerSync{Session: proto.Uint32(1), MaxBandwidth: proto.Uint32(128000)},
	} {
		if err := conn.WriteProto(message); err != nil {
			return err
		}
	}
	return nil
}
