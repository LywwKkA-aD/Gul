package mumble

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

const relayTestSecret = "arbitrary password with spaces"

// relayTestCredential caches the v2 bearer: PBKDF2 costs about 50 ms, so the
// whole package derives it once, exactly like a client derives it once per
// Connect instead of once per attempt.
var relayTestCredential = sync.OnceValue(func() relayproto.Credential {
	return relayproto.Derive([]byte(relayTestSecret))
})

// authorizeRelayRequest is the relay half of the contract: parse the header,
// insist on the v2 credential, compare in constant time.
func authorizeRelayRequest(w http.ResponseWriter, r *http.Request) bool {
	credential, ok := relayproto.ParseHeader(r.Header.Get("Authorization"))
	if !ok || credential.Legacy() || !credential.Matches(relayTestCredential()) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// relayAddress is the base address a client dials. It carries no tunnel path:
// where the tunnel answers is derived from the credential at dial time
// (relayproto.NamesFor).
func relayAddress(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return "wss" + strings.TrimPrefix(server.URL, "https")
}

// relayTestNames is the pair a server in these tests answers on, derived from
// the same credential the client uses.
func relayTestNames() relayproto.Names {
	return relayproto.NamesFor(relayTestCredential())
}

func TestDialWSSAuthenticatesAndCarriesBinaryStream(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != relayTestNames().Path {
			http.NotFound(w, r)
			return
		}
		if !authorizeRelayRequest(w, r) {
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{relayTestNames().Subprotocol}})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		stream := websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
		defer func() { _ = stream.Close() }()
		_, _ = io.Copy(stream, stream)
	}))
	t.Cleanup(server.Close)

	stream, err := dialWSS(t.Context(), relayAddress(t, server), relayTestCredential(), server.Client())
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

// TestDialWSSSendsTheDerivedCredentialNotThePassword pins the property the
// derivation exists for: a leaked header must not hand over the password.
func TestDialWSSSendsTheDerivedCredentialNotThePassword(t *testing.T) {
	headers := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case headers <- r.Header.Get("Authorization"):
		default:
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	_, _ = dialWSS(t.Context(), relayAddress(t, server), relayTestCredential(), server.Client())

	header := <-headers
	if strings.Contains(header, relayTestSecret) {
		t.Fatal("the Authorization header carried the server password")
	}
	credential, ok := relayproto.ParseHeader(header)
	if !ok {
		t.Fatalf("Authorization header %q is not a well-formed bearer", header)
	}
	if credential.Legacy() {
		t.Fatal("the legacy credential is still being sent")
	}
	if !credential.Matches(relayTestCredential()) {
		t.Fatal("the credential is not the one derived from the password")
	}
}

func TestRelayBearerIsDerivedOnlyForRelayEndpoints(t *testing.T) {
	cases := []struct {
		name     string
		address  string
		password string
		want     bool
	}{
		{name: "relay", address: "wss://murmur.example.test/mumble", password: relayTestSecret, want: true},
		{name: "relay without password", address: "wss://murmur.example.test/mumble"},
		{name: "direct", address: "murmur.example.test:64738", password: relayTestSecret},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bearer := relayBearer(credentials{address: tc.address, password: tc.password}, relayproto.Derive)
			if got := bearer != ""; got != tc.want {
				t.Fatalf("derived = %v, want %v", got, tc.want)
			}
			if tc.want && !bearer.Matches(relayTestCredential()) {
				t.Fatal("the relay bearer is not the v2 credential of the password")
			}
		})
	}
}

func TestDialWSSRejectsEmptyPasswordWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)

	if _, err := dialWSS(t.Context(), relayAddress(t, server), "", server.Client()); !errors.Is(err, ErrRelayPasswordRequired) {
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

	if _, err := dialWSS(t.Context(), relayAddress(t, server), "wrong", server.Client()); !errors.Is(err, ErrRelayAuthentication) {
		t.Fatalf("error = %v, want ErrRelayAuthentication", err)
	}
}

func TestDialWSSReportsRateLimitWithRetryAfter(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	_, err := dialWSS(t.Context(), relayAddress(t, server), relayTestCredential(), server.Client())

	if !errors.Is(err, ErrRelayRateLimited) {
		t.Fatalf("error = %v, want ErrRelayRateLimited", err)
	}
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("error %T does not carry the wait", err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %s, want 30s", limited.RetryAfter)
	}
	if isTerminalDialError(err) {
		t.Fatal("a rate limit is temporary, not terminal")
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "absent", header: ""},
		{name: "seconds", header: "30", want: 30 * time.Second},
		{name: "zero", header: "0"},
		{name: "negative", header: "-5"},
		{name: "garbage", header: "soon"},
		{name: "capped", header: "86400", want: maxRelayRetryAfter},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.header); got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tc.header, got, tc.want)
			}
		})
	}

	// The HTTP-date form is the other half of RFC 9110 §10.2.3.
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 60*time.Second || got > 90*time.Second {
		t.Fatalf("parseRetryAfter(%q) = %s, want about 90s", future, got)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Fatalf("parseRetryAfter(past) = %s, want 0", got)
	}
}

func TestDialWSSDoesNotFollowRedirectWithAuthorization(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests.Add(1)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+relayTestNames().Path, http.StatusFound)
	}))
	t.Cleanup(source.Close)

	if _, err := dialWSS(t.Context(), relayAddress(t, source), "secret", source.Client()); err == nil {
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

	if _, err := dialWSS(t.Context(), relayAddress(t, server), "secret", server.Client()); err == nil {
		t.Fatal("missing negotiated subprotocol was accepted")
	}
}

// TestDialWSSAppliesTheSharedMessageLimit covers the ordering dependency on
// websocket.NetConn, which disables the read limit on its way in: a message
// within the shared bound has to pass, anything above it has to fail.
func TestDialWSSAppliesTheSharedMessageLimit(t *testing.T) {
	cases := []struct {
		name      string
		size      int
		wantError bool
	}{
		// Above the 64 KiB the client used to allow: murmur's default
		// imagemessagelength alone is 128 KiB.
		{name: "image sized message", size: 128 << 10},
		{name: "at the limit", size: relayproto.MaxMessageBytes},
		// The library reads one byte past the limit to see the fin frame, so
		// the first size that must fail is two above it.
		{name: "over the limit", size: relayproto.MaxMessageBytes + 2, wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
					Subprotocols: []string{relayTestNames().Subprotocol},
				})
				if err != nil {
					return
				}
				defer func() { _ = conn.CloseNow() }()
				_ = conn.Write(r.Context(), websocket.MessageBinary, bytes.Repeat([]byte{0x7e}, tc.size))
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)

			stream, err := dialWSS(t.Context(), relayAddress(t, server), relayTestCredential(), server.Client())
			if err != nil {
				t.Fatalf("dial WSS: %v", err)
			}
			t.Cleanup(func() { stream.closeNow() })
			if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatalf("set read deadline: %v", err)
			}

			got := make([]byte, tc.size)
			n, err := io.ReadFull(stream, got)
			switch {
			case tc.wantError && err == nil:
				t.Fatalf("a %d byte message was accepted", tc.size)
			case !tc.wantError && err != nil:
				t.Fatalf("read %d byte message: %v", tc.size, err)
			case !tc.wantError && n != tc.size:
				t.Fatalf("read %d bytes, want %d", n, tc.size)
			}
		})
	}
}

// TestPacketConnOverWSSSendsOneMessagePerPacket verifies the premise the
// coalescing rests on: one Write on the adapted connection is one WebSocket
// message, so one packet must arrive as one message.
func TestPacketConnOverWSSSendsOneMessagePerPacket(t *testing.T) {
	messages := make(chan []byte, 8)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{relayTestNames().Subprotocol},
		})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			messages <- data
		}
	}))
	t.Cleanup(server.Close)

	stream, err := dialWSS(t.Context(), relayAddress(t, server), relayTestCredential(), server.Client())
	if err != nil {
		t.Fatalf("dial WSS: %v", err)
	}
	t.Cleanup(func() { stream.closeNow() })
	conn := newPacketConn(stream)

	voice := mumblePacket(1, bytes.Repeat([]byte{0x33}, 100))
	if _, err := conn.Write(voice[:packetHeaderBytes]); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := conn.Write(voice[packetHeaderBytes:]); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	ping := mumblePacket(3, []byte("ping"))
	if _, err := conn.Write(ping); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	for _, want := range [][]byte{voice, ping} {
		select {
		case got := <-messages:
			if !bytes.Equal(got, want) {
				t.Fatalf("message = %x, want %x", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for a message")
		}
	}
	select {
	case extra := <-messages:
		t.Fatalf("packet framing produced an extra message: %x", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestDialWSSReportsAFullRelayWithRetryAfter covers the answer a relay at
// capacity gives: 503 with a Retry-After. Without a type of its own it would
// reach the user as a raw English websocket error and its wait would be lost.
func TestDialWSSReportsAFullRelayWithRetryAfter(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	_, err := dialWSS(t.Context(), relayAddress(t, server), relayTestCredential(), server.Client())

	if !errors.Is(err, ErrRelayFull) {
		t.Fatalf("error = %v, want ErrRelayFull", err)
	}
	var full *RelayFullError
	if !errors.As(err, &full) {
		t.Fatalf("error %T does not carry the wait", err)
	}
	if full.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter = %s, want 5s", full.RetryAfter)
	}
	if errors.Is(err, ErrRelayRateLimited) {
		t.Fatal("a full relay is not this client asking too often")
	}
	if isTerminalDialError(err) {
		t.Fatal("a full relay clears on its own")
	}
}
