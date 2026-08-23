package relay

import (
	"bytes"
	"context"
	"io"
	"net"
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

func TestHandlerTemporarilyBansRepeatedAuthorizationFailures(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   2,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         30 * time.Second,
		MaxAuthTrackedSources:   16,
		Now:                     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		response := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, response.Code)
		}
		if got := response.Header().Get("Retry-After"); got != "" {
			t.Fatalf("attempt %d Retry-After = %q, want empty", attempt, got)
		}
	}

	limited := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d, want 429", limited.Code)
	}
	if got := limited.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
	if got := limited.Header().Get("Connection"); !strings.EqualFold(got, "close") {
		t.Fatalf("Connection = %q, want close", got)
	}
	if strings.Contains(limited.Body.String(), "wrong secret") {
		t.Fatal("response leaked the supplied credential")
	}

	now = now.Add(29*time.Second + time.Millisecond)
	limited = serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("status before ban expiry = %d, want 429", limited.Code)
	}
	if got := limited.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("rounded Retry-After = %q, want 1", got)
	}

	now = now.Add(time.Second)
	afterExpiry := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	if afterExpiry.Code != http.StatusUnauthorized {
		t.Fatalf("status after ban expiry = %d, want 401", afterExpiry.Code)
	}
}

func TestHandlerAuthorizationFailuresExpireOutsideWindow(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   1,
		AuthFailureWindow:       10 * time.Second,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   16,
		Now:                     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("first failure status = %d, want 401", got)
	}
	now = now.Add(10 * time.Second)
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("failure after window status = %d, want 401", got)
	}
}

func TestNewHandlerUsesSecureAuthorizationLimiterDefaults(t *testing.T) {
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if h.authFailures.failuresBeforeBan != 5 || h.authFailures.failureWindow != time.Minute || h.authFailures.banDuration != 5*time.Minute || h.authFailures.maxEntries != 4096 {
		t.Fatalf("unexpected limiter defaults: %#v", h.authFailures)
	}
}

func TestHandlerSuccessfulAuthorizationClearsPriorFailures(t *testing.T) {
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   2,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   16,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("first failure status = %d, want 401", got)
	}
	// A valid credential reaches the next protocol check and clears the source's
	// incomplete failure window before it becomes a ban.
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "server secret").Code; got != http.StatusBadRequest {
		t.Fatalf("valid authorization status = %d, want 400 from missing subprotocol", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("failure after success status = %d, want 401", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("second failure after success status = %d, want 401", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("third failure after success status = %d, want 429", got)
	}
}

func TestHandlerActiveBanRejectsValidAuthorizationUntilExpiry(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   1,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         30 * time.Second,
		MaxAuthTrackedSources:   16,
		Now:                     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("first failure status = %d, want 401", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("ban-triggering failure status = %d, want 429", got)
	}
	duringBan := serveAuthorizationAttempt(h, "192.0.2.10", "server secret")
	if duringBan.Code != http.StatusTooManyRequests {
		t.Fatalf("valid credential during ban status = %d, want 429", duringBan.Code)
	}
	if got := duringBan.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("valid credential Retry-After = %q, want 30", got)
	}

	now = now.Add(30 * time.Second)
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "server secret").Code; got != http.StatusBadRequest {
		t.Fatalf("valid credential after ban status = %d, want 400 from missing subprotocol", got)
	}
}

func TestHandlerAuthorizationLimiterHasBoundedMemory(t *testing.T) {
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   2,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   2,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	for _, source := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.1", "192.0.2.3"} {
		_ = serveAuthorizationAttempt(h, source, "wrong secret")
	}

	h.authFailures.mu.Lock()
	defer h.authFailures.mu.Unlock()
	if got := len(h.authFailures.entries); got != 2 {
		t.Fatalf("tracked sources = %d, want 2", got)
	}
	if _, ok := h.authFailures.entries["192.0.2.2"]; ok {
		t.Fatal("least-recently-used source was not evicted")
	}
	if _, ok := h.authFailures.entries["192.0.2.1"]; !ok {
		t.Fatal("recently used source was evicted")
	}
}

func TestHandlerAuthorizationLimiterIsConcurrent(t *testing.T) {
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   2,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   8,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	var group sync.WaitGroup
	for attempt := 0; attempt < 100; attempt++ {
		group.Add(1)
		go func(attempt int) {
			defer group.Done()
			source := net.IPv4(192, 0, 2, byte(attempt%16+1)).String()
			_ = serveAuthorizationAttempt(h, source, "wrong secret")
		}(attempt)
	}
	group.Wait()

	h.authFailures.mu.Lock()
	defer h.authFailures.mu.Unlock()
	if got := len(h.authFailures.entries); got > 8 {
		t.Fatalf("tracked sources = %d, want at most 8", got)
	}
}

func TestHandlerAuthorizationFailuresAreAtomicPerSource(t *testing.T) {
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   5,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   8,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	var unauthorized atomic.Int64
	var limited atomic.Int64
	var unexpected atomic.Int64
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			switch serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code {
			case http.StatusUnauthorized:
				unauthorized.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	group.Wait()
	if got := unauthorized.Load(); got != 5 {
		t.Fatalf("401 responses = %d, want exactly 5", got)
	}
	if got := limited.Load(); got != 95 {
		t.Fatalf("429 responses = %d, want 95", got)
	}
	if got := unexpected.Load(); got != 0 {
		t.Fatalf("unexpected responses = %d, want 0", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "server secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("valid credential during concurrent ban status = %d, want 429", got)
	}
}

func TestHandlerRelaysBinaryStream(t *testing.T) {
	upstream := echoServer(t)
	handler := mustHandler(t, Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                upstream,
		BearerCredential:        testBearerCredential("correct horse battery staple"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	conn, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader:   bearerHeader("correct horse battery staple"),
		Host:         "murmur.example.test",
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		t.Fatalf("dial relay: %v (response: %#v)", err, response)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	if got := conn.Subprotocol(); got != Subprotocol {
		t.Fatalf("subprotocol = %q, want %q", got, Subprotocol)
	}

	stream := websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
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
	handler := mustHandler(t, Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	for _, token := range []string{"", "wrong"} {
		t.Run(token, func(t *testing.T) {
			_, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
				HTTPHeader:   bearerHeader(token),
				Host:         "murmur.example.test",
				Subprotocols: []string{Subprotocol},
			})
			if err == nil {
				t.Fatal("dial unexpectedly succeeded")
			}
			if response == nil || response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %#v, want 401", response)
			}
		})
	}
}

func TestHandlerRequiresVersionedSubprotocol(t *testing.T) {
	handler := mustHandler(t, Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	_, response, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader: bearerHeader("server secret"),
		Host:       "murmur.example.test",
	})
	if err == nil {
		t.Fatal("dial unexpectedly succeeded")
	}
	if response == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %#v, want 400", response)
	}
}

func TestHandlerRejectsWrongPathQueryHostAndOriginBeforeDialingUpstream(t *testing.T) {
	handler := mustHandler(t, Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tests := []struct {
		name       string
		urlSuffix  string
		host       string
		origin     string
		wantStatus int
	}{
		{name: "path", urlSuffix: "/other", host: "murmur.example.test", wantStatus: http.StatusNotFound},
		{name: "query", urlSuffix: Path + "?target=elsewhere", host: "murmur.example.test", wantStatus: http.StatusBadRequest},
		{name: "host", urlSuffix: Path, host: "other.example.test", wantStatus: http.StatusMisdirectedRequest},
		{name: "origin", urlSuffix: Path, host: "murmur.example.test", origin: "https://evil.example.test", wantStatus: http.StatusForbidden},
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
				Subprotocols: []string{Subprotocol},
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
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		AuthFailuresBeforeBan:   1,
		AuthFailureWindow:       time.Minute,
		AuthBanDuration:         time.Minute,
		MaxAuthTrackedSources:   8,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

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
			request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test"+Path, nil)
			request.Host = "murmur.example.test"
			request.RemoteAddr = "192.0.2.10:12345"
			request.ContentLength = tc.contentLength
			request.TransferEncoding = tc.transferEncoding
			request.Body = io.NopCloser(panicReader{})
			request.Header.Set("Authorization", "Bearer definitely-wrong")
			response := httptest.NewRecorder()

			h.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
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
	upstream := echoServer(t)
	handler := mustHandler(t, Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                upstream,
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     1,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	opts := &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         "murmur.example.test",
		Subprotocols: []string{Subprotocol},
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
}

func TestHandlerShutdownClosesActiveWebSockets(t *testing.T) {
	upstream := echoServer(t)
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                upstream,
		BearerCredential:        testBearerCredential("server secret"),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         "murmur.example.test",
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, _, err := conn.Read(shutdownCtx); err == nil {
		t.Fatal("active WebSocket remained readable after shutdown")
	}
}

func TestHandlerRejectsTextAndOversizedMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    websocket.MessageType
		payload []byte
	}{
		{name: "text", kind: websocket.MessageText, payload: []byte("not a binary tunnel")},
		{name: "oversized", kind: websocket.MessageBinary, payload: bytes.Repeat([]byte{0xaa}, 1025)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := echoServer(t)
			h := mustHandler(t, Config{
				ExpectedHost:            "murmur.example.test",
				Upstream:                upstream,
				BearerCredential:        testBearerCredential("server secret"),
				MaxConnections:          4,
				MaxConnectionsPerIP:     2,
				MaxWebSocketMessageSize: 1024,
			})
			server := httptest.NewServer(h)
			t.Cleanup(server.Close)
			conn, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), &websocket.DialOptions{
				HTTPHeader:   bearerHeader("server secret"),
				Host:         "murmur.example.test",
				Subprotocols: []string{Subprotocol},
			})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			if err := conn.Write(t.Context(), tc.kind, tc.payload); err != nil {
				t.Fatalf("write test message: %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, _, err := conn.Read(ctx); err == nil {
				t.Fatal("invalid message was relayed")
			}
		})
	}
}

func TestNewHandlerRejectsUnsafeConfig(t *testing.T) {
	credential := testBearerCredential("secret")
	tests := []Config{
		{},
		{ExpectedHost: "murmur.example.test", Upstream: "example.com:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "localhost:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: credential, MaxConnections: 0, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "", Upstream: "127.0.0.1:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: []byte("raw-mumble-password"), MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: append(append([]byte(nil), credential...), '='), MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: append(append([]byte(nil), credential...), '\n'), MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: []byte(strings.Repeat("!", 43)), MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, AuthFailuresBeforeBan: -1},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, AuthFailureWindow: -1},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, AuthBanDuration: -1},
		{ExpectedHost: "murmur.example.test", Upstream: "127.0.0.1:64738", BearerCredential: credential, MaxConnections: 1, MaxConnectionsPerIP: 1, MaxWebSocketMessageSize: 1024, MaxAuthTrackedSources: -1},
	}
	for i, cfg := range tests {
		if _, err := NewHandler(cfg); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestHandlerDoesNotAcceptRawMumblePasswordAsBearerCredential(t *testing.T) {
	const rawPassword = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	h, err := NewHandler(Config{
		ExpectedHost:            "murmur.example.test",
		Upstream:                "127.0.0.1:9",
		BearerCredential:        testBearerCredential(rawPassword),
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test"+Path, nil)
	request.Host = "murmur.example.test"
	request.RemoteAddr = "192.0.2.10:12345"
	request.Header.Set("Authorization", "Bearer "+rawPassword)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestPseudonymousLoopbackIsStableAndSeparatesSourceIPs(t *testing.T) {
	key := sourceAddressKey([]byte("relay secret"))
	first := pseudonymousLoopback(key, "192.0.2.10")
	again := pseudonymousLoopback(key, "192.0.2.10")
	other := pseudonymousLoopback(key, "198.51.100.20")

	if !first.IsLoopback() {
		t.Fatalf("source = %s, want loopback", first)
	}
	if !first.Equal(again) {
		t.Fatalf("mapping is not stable: %s != %s", first, again)
	}
	if first.Equal(other) {
		t.Fatalf("different source IPs collided: %s", first)
	}
	if first.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatal("pseudonym must not use Murmur's ordinary loopback identity")
	}
}

func mustHandler(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return h
}

func echoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
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
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func bearerHeader(token string) http.Header {
	header := make(http.Header)
	if token != "" {
		header.Set("Authorization", relayproto.DeriveLegacy([]byte(token)).Header())
	}
	return header
}

func testBearerCredential(secret string) []byte {
	header := relayproto.DeriveLegacy([]byte(secret)).Header()
	return []byte(strings.TrimPrefix(header, "Bearer "))
}

func serveAuthorizationAttempt(handler http.Handler, sourceIP, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "https://murmur.example.test"+Path, nil)
	request.Host = "murmur.example.test"
	request.RemoteAddr = net.JoinHostPort(sourceIP, "12345")
	request.Header = bearerHeader(token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("request body must not be read")
}

func websocketURL(serverURL string) string {
	return "ws" + serverURL[len("http"):] + Path
}
