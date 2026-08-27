package relay

import (
	"net/http"
	"net/http/httptest"
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
	if got := dialNames(t, server, defaultTestSecret, names.Path, names.Subprotocol); got != http.StatusSwitchingProtocols {
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

// The relay answers on both contracts and lets the client choose. This is the
// whole compatibility mechanism for the shaped stream: no version byte inside
// the tunnel, no flag on the server, just the name the client asked for.
func TestTheClientChoosesTheContract(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = echoServer(t)
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	names := relayproto.NamesFor(testCredential(defaultTestSecret))
	for name, want := range map[string]string{
		"a client that offers only the plain stream":  names.Subprotocol,
		"a client that offers only the shaped stream": names.Shaped,
	} {
		t.Run(name, func(t *testing.T) {
			if got := dialNames(t, server, defaultTestSecret, names.Path, want); got != http.StatusSwitchingProtocols {
				t.Fatalf("status = %d, want 101", got)
			}
		})
	}

	// Offered together, the client's order decides, and a current client puts
	// the shaped one first.
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
