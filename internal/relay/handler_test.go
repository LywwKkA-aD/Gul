package relay

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

func TestHandlerRelaysBinaryStream(t *testing.T) {
	const secret = "correct horse battery staple"
	names := testNames(secret)
	cfg := baseConfig(secret)
	cfg.Upstream = echoServer(t)
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	conn, response, err := websocket.Dial(
		t.Context(),
		"ws"+server.URL[len("http"):]+names.Path,
		&websocket.DialOptions{
			HTTPHeader:   bearerHeader(secret),
			Host:         testHost,
			Subprotocols: []string{names.Shaped},
		},
	)
	if err != nil {
		t.Fatalf("dial relay: %v (response: %#v)", err, response)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	if got := conn.Subprotocol(); got != names.Shaped {
		t.Fatalf("subprotocol = %q, want %q", got, names.Shaped)
	}

	stream := relayproto.Shape(relayproto.AsMessageConn(websocket.NetConn(context.Background(), conn, websocket.MessageBinary)))
	t.Cleanup(func() { _ = stream.Close() })
	want := []byte{0x16, 0x03, 0x03, 0x00, 0x05, 0xde, 0xad, 0xbe, 0xef}
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

func TestHandlerRejectsMissingOrWrongBearerToken(t *testing.T) {
	server := httptest.NewServer(mustHandler(t, baseConfig("server secret")))
	t.Cleanup(server.Close)

	for _, token := range []string{"", "wrong"} {
		t.Run(token, func(t *testing.T) {
			_, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
				HTTPHeader:   bearerHeader(token),
				Host:         testHost,
				Subprotocols: []string{testSubprotocol()},
			})
			if err == nil {
				t.Fatal("dial unexpectedly succeeded")
			}
			if response == nil || response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %#v, want 404", response)
			}
		})
	}
}

func TestHandlerRequiresVersionedSubprotocol(t *testing.T) {
	server := httptest.NewServer(mustHandler(t, baseConfig("server secret")))
	t.Cleanup(server.Close)

	_, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader: bearerHeader("server secret"),
		Host:       testHost,
	})
	if err == nil {
		t.Fatal("dial unexpectedly succeeded")
	}
	if response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %#v, want 404", response)
	}
}

func TestHandlerRejectsWrongPathQueryHostAndOriginBeforeDialingUpstream(t *testing.T) {
	server := httptest.NewServer(mustHandler(t, baseConfig("server secret")))
	t.Cleanup(server.Close)

	tests := []struct {
		name       string
		urlSuffix  string
		host       string
		origin     string
		wantStatus int
	}{
		// Every one of these answers exactly like an address that does not
		// exist. Telling them apart used to hand a prober the whole decision
		// tree of the service (cover.go).
		{name: "path", urlSuffix: "/other", host: testHost, wantStatus: http.StatusNotFound},
		{name: "query", urlSuffix: testPath() + "?target=elsewhere", host: testHost, wantStatus: http.StatusNotFound},
		{name: "host", urlSuffix: testPath(), host: "other.example.test", wantStatus: http.StatusNotFound},
		{name: "origin", urlSuffix: testPath(), host: testHost, origin: "https://evil.example.test", wantStatus: http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := bearerHeader("server secret")
			if tc.origin != "" {
				header.Set("Origin", tc.origin)
			}
			url := "ws" + server.URL[len("http"):] + tc.urlSuffix
			_, response, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{
				HTTPHeader:   header,
				Host:         tc.host,
				Subprotocols: []string{testSubprotocol()},
			})
			if err == nil {
				t.Fatal("dial unexpectedly succeeded")
			}
			if response == nil || response.StatusCode != tc.wantStatus {
				t.Fatalf("status = %#v, want %d", response, tc.wantStatus)
			}
		})
	}
}

func TestHandlerRejectsGETBodiesWithoutReadingOrChargingAuthorizationLimiter(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 1
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.MaxAuthTrackedSources = 8
	h := mustHandler(t, cfg)

	tests := []struct {
		name             string
		contentLength    int64
		transferEncoding []string
	}{
		{name: "content length", contentLength: 1},
		{name: "chunked", contentLength: -1, transferEncoding: []string{"chunked"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://"+testHost+testPath(), nil)
			request.Host = testHost
			request.RemoteAddr = "192.0.2.10:12345"
			request.ContentLength = tc.contentLength
			request.TransferEncoding = tc.transferEncoding
			request.Body = io.NopCloser(panicReader{})
			request.Header.Set("Authorization", "Bearer definitely-wrong")
			response := httptest.NewRecorder()

			h.ServeHTTP(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.Code)
			}
			if got := response.Header().Get("Connection"); !strings.EqualFold(got, "close") {
				t.Fatalf("Connection = %q, want close", got)
			}
			if !request.Close {
				t.Fatal("request connection was not marked for closure")
			}
		})
	}

	h.authFailures.mu.Lock()
	defer h.authFailures.mu.Unlock()
	if got := len(h.authFailures.entries); got != 0 {
		t.Fatalf("body rejects charged %d authorization sources, want 0", got)
	}
}

func TestHandlerEnforcesPerIPConnectionLimit(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.MaxConnectionsPerIP = 1
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)
	opts := &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	}

	first, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	t.Cleanup(func() { _ = first.CloseNow() })

	_, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err == nil {
		t.Fatal("second dial unexpectedly succeeded")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %#v, want 503", response)
	}
	// A full relay tells the client when to come back instead of inviting an
	// immediate retry loop.
	if got := response.Header.Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}
}

func TestHandlerShutdownDrainsSessionsWithCloseFrame(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.ShutdownDrainTimeout = 3 * time.Second
	h := mustHandler(t, cfg)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	waitForActiveSessions(t, h, 1)

	readErr := make(chan error, 1)
	go func() {
		_, _, err := conn.Read(context.Background())
		readErr <- err
	}()

	shutdownCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	if err := h.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	elapsed := time.Since(started)

	select {
	case err := <-readErr:
		if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
			t.Fatalf("close status = %d (%v), want %d", got, err, websocket.StatusGoingAway)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never observed the close frame")
	}
	if elapsed >= cfg.ShutdownDrainTimeout {
		t.Fatalf("shutdown took %s: sessions were cut off instead of drained", elapsed)
	}
}

func TestHandlerShutdownForcesSessionsPastTheDrainWindow(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.ShutdownDrainTimeout = 50 * time.Millisecond
	h := mustHandler(t, cfg)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	// This client never reads, so it never answers the close frame.
	conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	waitForActiveSessions(t, h, 1)

	shutdownCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	h.mu.Lock()
	remaining := len(h.active)
	h.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("active sessions after shutdown = %d, want 0", remaining)
	}
}

func TestHandlerRejectsTextAndOversizedMessages(t *testing.T) {
	const limit = 1024
	for _, tc := range []struct {
		name    string
		kind    websocket.MessageType
		payload []byte
		relayed bool
		// refusal is the close status the relay must answer with, where one is
		// promised. The oversized case cannot be judged by "nothing came back"
		// either: the library enforces the limit as the message streams
		// (limitReader errors on the read after the budget runs out), so a
		// prefix can reach the upstream and be echoed back before the close
		// lands.
		//
		// Zero means only that the session must end. Since the tunnel is framed
		// (relayproto.Shape), an oversized raw message is read as a frame with
		// a header that is not one, and which error wins - the malformed frame
		// or the size limit - is a race between two layers. Both refuse it and
		// both bound the memory, so promising one of them would be promising
		// something we do not control.
		refusal websocket.StatusCode
	}{
		{
			name:    "text",
			kind:    websocket.MessageText,
			payload: []byte("not a binary tunnel"),
			refusal: websocket.StatusUnsupportedData,
		},
		{
			name:    "oversized",
			kind:    websocket.MessageBinary,
			payload: bytes.Repeat([]byte{0xaa}, limit+1),
		},
		{name: "a large message", kind: websocket.MessageBinary, payload: bytes.Repeat([]byte{0xbb}, limit/2), relayed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig("server secret")
			cfg.Upstream = echoServer(t)
			cfg.MaxWebSocketMessageSize = limit
			server := httptest.NewServer(mustHandler(t, cfg))
			t.Cleanup(server.Close)
			conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
				HTTPHeader:   bearerHeader("server secret"),
				Host:         testHost,
				Subprotocols: []string{testSubprotocol()},
			})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			// The refused cases are written raw: they are about what the
			// WebSocket layer accepts, which is decided before the framing
			// above it ever sees a byte. The relayed one goes through the
			// framing, because that is what a client actually sends.
			if tc.relayed {
				out := relayproto.Shape(relayproto.AsMessageConn(websocket.NetConn(t.Context(), conn, websocket.MessageBinary)))
				if _, err := out.Write(tc.payload); err != nil {
					t.Fatalf("write test message: %v", err)
				}
			} else if err := conn.Write(t.Context(), tc.kind, tc.payload); err != nil {
				t.Fatalf("write test message: %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if !tc.relayed {
				for {
					_, _, err := conn.Read(ctx)
					if err == nil {
						continue // an echoed prefix; the close is still owed
					}
					if tc.refusal == 0 {
						return // the session ended, which is all that is promised
					}
					if got := websocket.CloseStatus(err); got != tc.refusal {
						t.Fatalf("connection ended with close status %v (%v), want %v",
							got, err, tc.refusal)
					}
					return
				}
			}
			// The upstream may split its answer across reads, so the echo is
			// consumed as a stream rather than as one message.
			stream := relayproto.Shape(relayproto.AsMessageConn(websocket.NetConn(ctx, conn, websocket.MessageBinary)))

			echoed := make([]byte, len(tc.payload))
			if _, err := io.ReadFull(stream, echoed); err != nil {
				t.Fatalf("message at the limit was not relayed: %v", err)
			}
			if !bytes.Equal(echoed, tc.payload) {
				t.Fatalf("echo differs from the payload (%d bytes)", len(echoed))
			}
		})
	}
}

// TestHandlerAppliesTheSharedMessageBound pins the read limit to the value both
// ends of the contract apply. websocket.NetConn disables the limit, so the
// order of the two calls in ServeHTTP is what makes this hold.
func TestHandlerAppliesTheSharedMessageBound(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.MaxWebSocketMessageSize = relayproto.MaxMessageBytes
	h := mustHandler(t, cfg)
	if h.messageSize != relayproto.MaxMessageBytes {
		t.Fatalf("message size = %d, want %d", h.messageSize, relayproto.MaxMessageBytes)
	}
}

func TestNewHandlerRejectsUnsafeConfig(t *testing.T) {
	credentials := []relayproto.Credential{testCredential("secret")}
	tests := []Config{
		{},
		{ExpectedHost: testHost, Upstream: "example.com:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "localhost:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 0, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "", Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: []relayproto.Credential{"raw-mumble-password!"}, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: []relayproto.Credential{credentials[0] + "="}, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: []relayproto.Credential{credentials[0] + "\n"}, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 0},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 2, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, AuthFailuresBeforeBan: -1},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, AuthFailureWindow: -1},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, AuthBanDuration: -1},
		{ExpectedHost: testHost, Upstream: "127.0.0.1:64738", BearerCredentials: credentials, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, MaxAuthTrackedSources: -1},
	}
	for i, cfg := range tests {
		if _, err := NewHandler(cfg); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}
