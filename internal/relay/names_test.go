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
	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Shaped); got != http.StatusSwitchingProtocols {
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
	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Shaped); got != http.StatusSwitchingProtocols {
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
			Subprotocols: []string{names.Shaped, names.Subprotocol},
		})
	if err != nil {
		t.Fatalf("dial with both offered: %v (status %v)", err, response)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	if got := conn.Subprotocol(); got != names.Shaped {
		t.Fatalf("negotiated %q, want the shaped contract %q", got, names.Shaped)
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

	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Shaped); got != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", got)
	}
	attrs := recordAttrs(records.await(t, "relay session opened"))
	if attrs["contract"] != contractShaped {
		t.Fatalf("logged contract = %q, want %q", attrs["contract"], contractShaped)
	}
	if rendered := records.rendered(); strings.Contains(rendered, names.Shaped) {
		t.Fatalf("the log carried the derived name itself: %s", rendered)
	}
}
