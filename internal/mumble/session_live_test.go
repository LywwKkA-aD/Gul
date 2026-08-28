//go:build live

package mumble

import (
	"log/slog"
	"testing"
	"time"
)

// TestDialLocalMurmur is a smoke test against the local dev stand
// (task murmur:up). It is excluded from CI: run with `go test -tags live`.
func TestDialLocalMurmur(t *testing.T) {
	tofu := NewTOFUStore(t.TempDir(), slog.Default())
	// Through a relay, because that is the only road there is now: the direct
	// host:port one was removed with the tunnel contract, so a client and a
	// Murmur on the same machine still meet through one.
	ep, roots, _ := localRelay(t)

	s, err := Dial(DialConfig{
		Address:    ep.address,
		Username:   "gul-smoke",
		Password:   relayLiveSecret,
		OuterRoots: roots,
	}, tofu, slog.Default())
	if err != nil {
		t.Fatalf("dial local murmur (is the stand up? task murmur:up): %v", err)
	}
	defer s.Disconnect()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == "synced" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("session did not reach synced state, last state: %s", s.State())
}
