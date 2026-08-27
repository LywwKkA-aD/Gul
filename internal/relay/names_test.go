package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// dialNames attempts a handshake with a chosen path and subprotocol and
// reports the status the relay answered with.
func dialNames(t *testing.T, server *httptest.Server, secret, path, subprotocol string) int {
	t.Helper()
	conn, response, err := websocket.Dial(
		t.Context(),
		"ws"+server.URL[len("http"):]+path,
		&websocket.DialOptions{
			HTTPHeader:   bearerHeader(secret),
			Host:         testHost,
			Subprotocols: []string{subprotocol},
		},
	)
	if err == nil {
		t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
		return http.StatusSwitchingProtocols
	}
	if response == nil {
		t.Fatalf("dial failed without a response: %v", err)
	}
	return response.StatusCode
}

// The names builds up to v0.4.0-alpha.2 used. Written out rather than imported:
// nothing in the code refers to them any more, and this test exists to pin that
// these particular historical strings get no answer.
const (
	retiredPath        = "/mumble"
	retiredSubprotocol = "gul-mumble-v1"
)

// The old pair is not merely refused: it has to be refused exactly like an
// address that does not exist. A refusal of its own would be the signal that
// this host used to be a relay - which is what the derived names removed.
func TestRetiredNamesAreRefusedLikeAnyUnknownAddress(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = echoServer(t)
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	names := testNames(defaultTestSecret)
	cases := map[string]struct{ path, subprotocol string }{
		"the old pair":              {retiredPath, retiredSubprotocol},
		"the old path":              {retiredPath, names.Subprotocol},
		"the old subprotocol":       {names.Path, retiredSubprotocol},
		"the retired plain stream":  {names.Path, names.Subprotocol},
		"the retired shaped stream": {names.Path, relayproto.NamesFor(testCredential(defaultTestSecret)).Shaped},
		"an address nobody serves":  {"/does-not-exist", names.Subprotocol},
		"the pair of another relay": {relayproto.NamesFor(testCredential("elsewhere")).Path, names.Subprotocol},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := dialNames(t, server, defaultTestSecret, tc.path, tc.subprotocol); got != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", got)
			}
		})
	}

	// The current pair still works, so the refusals above are about the names
	// and not about a relay that stopped accepting anything.
	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Tunnel); got != http.StatusSwitchingProtocols {
		t.Fatalf("current pair status = %d, want 101", got)
	}
}

// A relay answers on the names its own credential produces and on no others:
// knowing one server's path must not locate another, and no fixed name is
// mounted alongside them.
func TestTunnelPathsFollowTheCredentials(t *testing.T) {
	t.Parallel()
	h := mustHandler(t, baseConfig(defaultTestSecret))

	paths := h.TunnelPaths()
	want := testNames(defaultTestSecret).Path
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("paths = %v, want exactly [%s]", paths, want)
	}
}

// One contract left, and a client that knows only the older one gets nothing.
//
// The plain byte stream was retired on 2026-08-27. A current client offers both
// names, newest first, so it is unaffected; a client that predates the shaped
// contract offers only the old name and is answered exactly as an address
// nobody serves - a refusal of its own would say this host used to answer
// there, which is the leak the derived names removed in the first place.
func TestOnlyTheShapedContractIsAnswered(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = echoServer(t)
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	names := relayproto.NamesFor(testCredential(defaultTestSecret))
	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Tunnel); got != http.StatusSwitchingProtocols {
		t.Fatalf("the shaped contract got %d, want 101", got)
	}
	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Subprotocol); got != http.StatusNotFound {
		t.Fatalf("the retired plain contract got %d, want the 404 any unknown address gets", got)
	}

	// A current client offers both, and still lands on the shaped one.
	conn, response, err := websocket.Dial(t.Context(),
		"ws"+server.URL[len("http"):]+names.Path,
		&websocket.DialOptions{
			HTTPHeader:   bearerHeader(defaultTestSecret),
			Host:         testHost,
			Subprotocols: []string{names.Tunnel, names.Subprotocol},
		})
	if err != nil {
		t.Fatalf("dial with both offered: %v (status %v)", err, response)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	if got := conn.Subprotocol(); got != names.Tunnel {
		t.Fatalf("negotiated %q, want the shaped contract %q", got, names.Tunnel)
	}
}

// The session log names the contract. It is what the retirement of the plain
// stream was decided on, and what the next such decision will be read from.
// The names themselves must never appear: they are derived from the password.
func TestTheSessionLogNamesTheContract(t *testing.T) {
	t.Parallel()
	names := relayproto.NamesFor(testCredential(defaultTestSecret))
	logger, records := newRecordingLogger()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = echoServer(t)
	cfg.Logger = logger
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	completeTunnel(t, dialTunnelRoad(t, server, defaultTestSecret))

	attrs := recordAttrs(records.await(t, "relay session opened"))
	if attrs["contract"] != contractTunnel {
		t.Fatalf("logged contract = %q, want %q", attrs["contract"], contractTunnel)
	}
	if rendered := records.rendered(); strings.Contains(rendered, names.Tunnel) {
		t.Fatalf("the log carried the derived name itself: %s", rendered)
	}
}

// Every refusal names its shape in the journal, and none of them names the
// value.
//
// This was a real gap, found while diagnosing a user whose client reported
// "wrong address or password" while the relay's journal was empty for those
// seconds. Seven refusal paths answered with the cover site and logged nothing,
// so a request arriving mangled - a stripped Sec-WebSocket-Protocol header, a
// rewritten Origin, a proxy changing the method - was indistinguishable from a
// request that never arrived at all.
func TestEveryRefusalSaysWhy(t *testing.T) {
	t.Parallel()
	names := relayproto.NamesFor(testCredential(defaultTestSecret))
	tests := map[string]struct {
		want    string
		prepare func(*http.Request)
	}{
		refusedPath: {refusedPath, func(r *http.Request) {
			r.URL.Path = "/somewhere-else"
		}},
		refusedQuery: {refusedQuery, func(r *http.Request) {
			r.URL.RawQuery = "a=1"
		}},
		refusedHost: {refusedHost, func(r *http.Request) {
			r.Host = "somebody.else.test"
		}},
		refusedMethod: {refusedMethod, func(r *http.Request) {
			r.Method = http.MethodPost
		}},
		refusedBody: {refusedBody, func(r *http.Request) {
			r.ContentLength = 7
		}},
		refusedOrigin: {refusedOrigin, func(r *http.Request) {
			r.Header.Set("Origin", "https://somebody.else.test")
		}},
		refusedSubprotocol: {refusedSubprotocol, func(r *http.Request) {
			r.Header.Set("Sec-WebSocket-Protocol", "something-we-do-not-answer-on")
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			logger, records := newRecordingLogger()
			cfg := baseConfig(defaultTestSecret)
			cfg.Logger = logger
			h := mustHandler(t, cfg)

			request := httptest.NewRequest(http.MethodGet, "https://"+testHost+names.Path, nil)
			request.Host = testHost
			request.RemoteAddr = "192.0.2.10:12345"
			request.Header = bearerHeader(defaultTestSecret)
			request.Header.Set("Sec-WebSocket-Protocol", names.Tunnel)
			tc.prepare(request)

			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want the same 404 every refusal gives", response.Code)
			}
			attrs := recordAttrs(records.await(t, "relay request refused"))
			if attrs["reason"] != tc.want {
				t.Fatalf("logged reason = %q, want %q", attrs["reason"], tc.want)
			}
			// The names come from the password. A journal is not a place for them.
			if rendered := records.rendered(); strings.Contains(rendered, names.Tunnel) ||
				strings.Contains(rendered, names.Path) {
				t.Fatalf("the log carried a derived name: %s", rendered)
			}
		})
	}
}
