package mumble

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

func TestDialWSSAuthenticatesAndCarriesBinaryStream(t *testing.T) {
	secret := "arbitrary password with spaces"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != relayproto.Path {
			http.NotFound(w, r)
			return
		}
		if !relayproto.MatchesAuthorization(r.Header.Get("Authorization"), []byte(secret)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{relayproto.Subprotocol}})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		stream := websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
		defer func() { _ = stream.Close() }()
		_, _ = io.Copy(stream, stream)
	}))
	t.Cleanup(server.Close)

	address := "wss" + strings.TrimPrefix(server.URL, "https") + relayproto.Path
	stream, err := dialWSS(t.Context(), address, secret, server.Client())
	if err != nil {
		t.Fatalf("dial WSS: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	want := []byte{0x16, 0x03, 0x03, 0xde, 0xad, 0xbe, 0xef}
	if _, err := stream.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("echo = %x, want %x", got, want)
	}
}

func TestDialWSSRejectsEmptyPasswordWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	address := "wss" + strings.TrimPrefix(server.URL, "https") + relayproto.Path

	if _, err := dialWSS(t.Context(), address, "", server.Client()); !errors.Is(err, ErrRelayPasswordRequired) {
		t.Fatalf("error = %v, want ErrRelayPasswordRequired", err)
	}
	if requests.Load() != 0 {
		t.Fatal("empty password reached the network")
	}
}

func TestDialWSSMapsUnauthorizedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	address := "wss" + strings.TrimPrefix(server.URL, "https") + relayproto.Path

	if _, err := dialWSS(t.Context(), address, "wrong", server.Client()); !errors.Is(err, ErrRelayAuthentication) {
		t.Fatalf("error = %v, want ErrRelayAuthentication", err)
	}
}

func TestDialWSSDoesNotFollowRedirectWithAuthorization(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests.Add(1)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+relayproto.Path, http.StatusFound)
	}))
	t.Cleanup(source.Close)
	client := source.Client()
	address := "wss" + strings.TrimPrefix(source.URL, "https") + relayproto.Path

	if _, err := dialWSS(t.Context(), address, "secret", client); err == nil {
		t.Fatal("redirect unexpectedly succeeded")
	}
	if redirectedRequests.Load() != 0 {
		t.Fatal("authorization-bearing request followed the redirect")
	}
}

func TestDialWSSRequiresNegotiatedSubprotocol(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err == nil {
			defer func() { _ = conn.CloseNow() }()
			<-r.Context().Done()
		}
	}))
	t.Cleanup(server.Close)
	address := "wss" + strings.TrimPrefix(server.URL, "https") + relayproto.Path

	if _, err := dialWSS(t.Context(), address, "secret", server.Client()); err == nil {
		t.Fatal("missing negotiated subprotocol was accepted")
	}
}
