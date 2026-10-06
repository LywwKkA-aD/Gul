package hysteria

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestDialFallsBackFromSilentIPv6ToIPv4(t *testing.T) {
	cfg := startTestServer(t, "", nil)
	_, portText, err := net.SplitHostPort(cfg.Server)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	blackhole, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer blackhole.Close()
	target, _ := startEchoBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.IPv6loopback}, {IP: net.IPv4(127, 0, 0, 1)}}, nil
	}
	conn, err := dialWithLookup(ctx, cfg, target, lookup)
	if err != nil {
		t.Fatalf("working IPv4 address was not reached before deadline: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	greeting := make([]byte, len("ready"))
	if _, err := io.ReadFull(conn, greeting); err != nil || string(greeting) != "ready" {
		t.Fatalf("fallback stream greeting = %q, %v", greeting, err)
	}
	_ = blackhole.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := blackhole.ReadFromUDP(make([]byte, 4096)); err != nil {
		t.Fatalf("test did not exercise IPv6 handshake attempt: %v", err)
	}
}

func TestAddressFallbackStopsOnAuthenticationOrCertificateRejection(t *testing.T) {
	for _, rejection := range []error{
		ErrAuthentication,
		x509.UnknownAuthorityError{},
		x509.HostnameError{},
		x509.CertificateInvalidError{},
	} {
		t.Run(errorType(rejection), func(t *testing.T) {
			var attempts atomic.Int32
			addresses := []net.IPAddr{{IP: net.IPv6loopback}, {IP: net.IPv4(127, 0, 0, 1)}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := raceAddresses(ctx, addresses, func(context.Context, net.IPAddr) (net.Conn, error) {
				attempts.Add(1)
				return nil, rejection
			})
			if conn != nil || !errors.Is(err, rejection) {
				t.Fatalf("result = %v, %T; want original rejection", conn, err)
			}
			if got := attempts.Load(); got != 1 {
				t.Fatalf("tried %d addresses after rejection, want 1", got)
			}
		})
	}
}

func TestAddressFallbackLimitsConcurrencyAndClosesLosingConnection(t *testing.T) {
	addresses := []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("::2")}, {IP: net.ParseIP("127.0.0.1")}}
	firstStarted := make(chan struct{})
	loser, peer := net.Pipe()
	defer peer.Close()
	winner, otherPeer := net.Pipe()
	defer otherPeer.Close()
	var active, maximum, attempts atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := raceAddresses(ctx, addresses, func(attemptCtx context.Context, address net.IPAddr) (net.Conn, error) {
		attempts.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		if address.IP.Equal(net.IPv6loopback) {
			close(firstStarted)
			<-attemptCtx.Done()
			return loser, nil
		}
		if address.IP.To4() == nil {
			return nil, errors.New("expected alternate address family")
		}
		<-firstStarted
		return winner, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn != winner {
		t.Fatal("wrong address attempt won")
	}
	if maximum.Load() > 2 || attempts.Load() != 2 || active.Load() != 0 {
		t.Fatalf("attempt lifecycle = active %d, maximum %d, attempts %d", active.Load(), maximum.Load(), attempts.Load())
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("losing successful connection was not closed: %v", err)
	}
}

func errorType(err error) string {
	switch err.(type) {
	case x509.UnknownAuthorityError:
		return "unknown-authority"
	case x509.HostnameError:
		return "hostname"
	case x509.CertificateInvalidError:
		return "invalid-certificate"
	default:
		return "authentication"
	}
}
