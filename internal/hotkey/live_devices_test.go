//go:build devices

package hotkey

import (
	"os"
	"testing"
	"time"
)

// Manual check against a real keyboard. It is excluded from `task test` on
// purpose: it needs a person at the machine.
//
//	go test -tags devices -run TestLiveHoldToTalk -v ./internal/hotkey
//
// Set GUL_PTT_KEY to test a key other than Space.

func TestLiveHoldToTalk(t *testing.T) {
	key := os.Getenv("GUL_PTT_KEY")
	if key == "" {
		key = "Space"
	}

	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()

	capability := m.Capability()
	t.Logf("mode = %s", capability.Mode)
	if capability.Reason != "" {
		t.Logf("reason = %s", capability.Reason)
	}
	if capability.Mode != ModeHold {
		t.Skipf("this session cannot read key state; nothing to hold")
	}

	transitions := make(chan bool, 16)
	if err := m.Watch(key, func(held bool) { transitions <- held }); err != nil {
		t.Fatalf("Watch(%q): %v", key, err)
	}

	t.Logf("hold %s for about a second, then let go (20 s to comply)", key)
	deadline := time.After(20 * time.Second)

	var pressedAt time.Time
	for {
		select {
		case held := <-transitions:
			if held {
				pressedAt = time.Now()
				t.Logf("held")
				continue
			}
			if pressedAt.IsZero() {
				t.Fatal("a release arrived without a press")
			}
			t.Logf("released after %s", time.Since(pressedAt).Round(time.Millisecond))
			return
		case <-deadline:
			if pressedAt.IsZero() {
				t.Fatalf("no press of %q was observed: the backend reports nothing", key)
			}
			t.Fatalf("%q was pressed but never reported released", key)
		}
	}
}
