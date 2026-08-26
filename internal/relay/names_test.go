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

// The pair that named the contents has to keep working while the clients that
// predate the change are still out there, and every use has to be visible so
// the operator knows when the window can be shut.
func TestLegacyNamesWorkWhileTheWindowIsOpenAndAreLogged(t *testing.T) {
	t.Parallel()
	logger, records := newRecordingLogger()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = echoServer(t)
	cfg.AcceptLegacyNames = true
	cfg.Logger = logger
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	got := dialNames(t, server, defaultTestSecret, relayproto.LegacyPath, relayproto.LegacySubprotocol)
	if got != http.StatusSwitchingProtocols {
		t.Fatalf("legacy handshake status = %d, want 101", got)
	}
	records.await(t, "relay accepted the legacy tunnel names")
}

// Once the window is shut the old pair is not merely refused: it has to be
// refused exactly like an address that does not exist, or shutting it would
// itself become the signal that this host used to be a relay.
func TestLegacyNamesAreRefusedLikeAnyUnknownAddress(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.Upstream = echoServer(t)
	cfg.AcceptLegacyNames = false
	server := httptest.NewServer(mustHandler(t, cfg))
	t.Cleanup(server.Close)

	names := testNames(defaultTestSecret)
	cases := map[string]struct{ path, subprotocol string }{
		"the old pair":              {relayproto.LegacyPath, relayproto.LegacySubprotocol},
		"the old path":              {relayproto.LegacyPath, names.Subprotocol},
		"the old subprotocol":       {names.Path, relayproto.LegacySubprotocol},
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
// knowing one server's path must not locate another.
func TestTunnelPathsFollowTheCredentials(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(defaultTestSecret)
	cfg.AcceptLegacyNames = false
	h := mustHandler(t, cfg)

	paths := h.TunnelPaths()
	want := testNames(defaultTestSecret).Path
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("paths = %v, want exactly [%s]", paths, want)
	}

	cfg.AcceptLegacyNames = true
	withLegacy := mustHandler(t, cfg).TunnelPaths()
	if len(withLegacy) != 2 {
		t.Fatalf("paths with the window open = %v, want the derived and the legacy one", withLegacy)
	}
}
