package relay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

const testHost = "murmur.example.test"

var (
	credentialCacheMu sync.Mutex
	credentialCache   = make(map[string]relayproto.Credential)
)

// testCredential derives at most once per secret. The v2 derivation costs tens
// of milliseconds by design, so a suite that repeats it per case spends
// seconds in PBKDF2.
func testCredential(secret string) relayproto.Credential {
	credentialCacheMu.Lock()
	defer credentialCacheMu.Unlock()
	credential, ok := credentialCache[secret]
	if !ok {
		credential = relayproto.Derive([]byte(secret))
		credentialCache[secret] = credential
	}
	return credential
}

func testLegacyCredential(secret string) relayproto.Credential {
	return relayproto.DeriveLegacy([]byte(secret))
}

func baseConfig(secret string) Config {
	return Config{
		ExpectedHost:            testHost,
		Upstream:                "127.0.0.1:9",
		BearerCredentials:       []relayproto.Credential{testCredential(secret)},
		MaxConnections:          4,
		MaxConnectionsPerIP:     2,
		MaxWebSocketMessageSize: 64 * 1024,
		Logger:                  slog.New(slog.DiscardHandler),
	}
}

func mustHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return h
}

func echoServer(t *testing.T) string {
	t.Helper()
	return upstreamServer(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})
}

func upstreamServer(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
				serve(conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func bearerHeader(token string) http.Header {
	header := make(http.Header)
	if token != "" {
		header.Set("Authorization", testCredential(token).Header())
	}
	return header
}

func serveAuthorizationAttempt(handler http.Handler, sourceIP, token string) *httptest.ResponseRecorder {
	return serveWithHeader(handler, sourceIP, bearerHeader(token))
}

func serveWithHeader(handler http.Handler, sourceIP string, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "https://"+testHost+Path, nil)
	request.Host = testHost
	request.RemoteAddr = net.JoinHostPort(sourceIP, "12345")
	request.Header = header
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// waitForActiveSessions blocks until the handler has registered the expected
// number of sessions. A successful dial only proves the handshake completed;
// registration happens a few statements later in the session goroutine.
func waitForActiveSessions(t *testing.T, h *Handler, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		got := len(h.active)
		h.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("active sessions = %d, want %d", got, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func websocketURL(serverURL string) string {
	return "ws" + serverURL[len("http"):] + Path
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("request body must not be read")
}

// recordingHandler captures structured log records so tests can assert which
// events the relay emits and that none of them carries a credential.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func newRecordingLogger() (*slog.Logger, *recordingHandler) {
	handler := &recordingHandler{}
	return slog.New(handler), handler
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func (h *recordingHandler) find(message string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		if record.Message == message {
			return record, true
		}
	}
	return slog.Record{}, false
}

func (h *recordingHandler) await(t *testing.T, message string) slog.Record {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if record, ok := h.find(message); ok {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("log record %q was not emitted; recorded: %v", message, h.messages())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *recordingHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages := make([]string, 0, len(h.records))
	for _, record := range h.records {
		messages = append(messages, record.Message)
	}
	return messages
}

func (h *recordingHandler) rendered() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []byte
	for _, record := range h.records {
		out = append(out, record.Message...)
		record.Attrs(func(attr slog.Attr) bool {
			out = append(out, ' ')
			out = append(out, attr.Key...)
			out = append(out, '=')
			out = append(out, attr.Value.String()...)
			return true
		})
		out = append(out, '\n')
	}
	return string(out)
}

func recordAttrs(record slog.Record) map[string]string {
	attrs := make(map[string]string, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.String()
		return true
	})
	return attrs
}
