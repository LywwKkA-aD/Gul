package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

// alpnProbe answers TLS with the protocols it is told to prefer and reports
// what the client offered.
func alpnProbe(t *testing.T, prefer []string) (addr string, offered chan []string, roots *tls.Config) {
	t.Helper()
	const host = "probe.example.test"
	certificate, pool := testServerCertificate(t, host, 77)

	offered = make(chan []string, 4)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   prefer,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			select {
			case offered <- hello.SupportedProtos:
			default:
			}
			return nil, nil
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
			go func() {
				_ = conn.(*tls.Conn).HandshakeContext(context.Background())
				time.Sleep(20 * time.Millisecond)
				_ = conn.Close()
			}()
		}
	}()
	return listener.Addr().String(), offered, &tls.Config{RootCAs: pool, ServerName: host}
}

// What a middlebox reads in the clear is the ClientHello and almost nothing
// else: the request, its headers and their order are all inside TLS. So the
// ALPN list is one of the few things worth getting right, and Chrome's is two
// entries long.
//
// This pins the default, and it exists because the default was briefly one
// entry - which bought support for a CDN and paid for it by making every
// connection to every server look unlike a browser.
func TestTheHandshakeOffersWhatChromeOffers(t *testing.T) {
	t.Parallel()
	addr, offered, roots := alpnProbe(t, []string{"http/1.1"})

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, err := dialBrowserTLS(ctx, "tcp", addr, roots, (&net.Dialer{}).DialContext, false)
	if err != nil {
		t.Fatalf("рукопожатие: %v", err)
	}
	defer conn.Close()

	select {
	case protos := <-offered:
		if len(protos) != 2 || protos[0] != "h2" || protos[1] != "http/1.1" {
			t.Fatalf("предложено %v, ожидалось [h2 http/1.1] - как у Chrome", protos)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("сервер не увидел ClientHello")
	}
}

// A front that speaks h2 leaves this client nothing to do with the connection,
// and it has to say so by name. Left to the WebSocket layer it surfaces as a
// parse failure on the server's first SETTINGS frame - binary that is not a
// reply at all, reported as "malformed HTTP response".
func TestAnHTTP2AnswerIsNamedRatherThanParsed(t *testing.T) {
	t.Parallel()
	addr, _, roots := alpnProbe(t, []string{"h2", "http/1.1"})

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, err := dialBrowserTLS(ctx, "tcp", addr, roots, (&net.Dialer{}).DialContext, false)
	if err == nil {
		_ = conn.Close()
		t.Fatal("h2 был принят молча")
	}
	if !errors.Is(err, errHTTP2Negotiated) {
		t.Fatalf("получено %v, ожидалось errHTTP2Negotiated", err)
	}
}

// And the retry gets through the same front, because it stops offering the one
// protocol it cannot use. Only this attempt deviates from the browser.
func TestTheRetryDropsHTTP2AndConnects(t *testing.T) {
	t.Parallel()
	addr, offered, roots := alpnProbe(t, []string{"h2", "http/1.1"})

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, err := dialBrowserTLS(ctx, "tcp", addr, roots, (&net.Dialer{}).DialContext, true)
	if err != nil {
		t.Fatalf("повторная попытка не прошла: %v", err)
	}
	defer conn.Close()

	select {
	case protos := <-offered:
		if len(protos) != 1 || protos[0] != "http/1.1" {
			t.Fatalf("повтор предложил %v, ожидалось только [http/1.1]", protos)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("сервер не увидел ClientHello")
	}
}
