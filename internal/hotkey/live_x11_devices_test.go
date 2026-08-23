//go:build linux && cgo && !server && devices

package hotkey

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// End-to-end check of the X11 backend against a running server, driving the
// keyboard with XTEST rather than a finger so it can run unattended:
//
//	Xvfb :99 & DISPLAY=:99 go test -tags devices -run TestLiveX11 -v ./internal/hotkey
//
// XTEST input goes through the server's core input processing, so it lands in
// the same keymap vector XQueryKeymap reads - which is exactly the path a real
// key takes.
func TestLiveX11ObservesASynthesizedKey(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no DISPLAY: start an X server first")
	}
	xdotool, err := exec.LookPath("xdotool")
	if err != nil {
		t.Skip("xdotool is needed to synthesize the key press")
	}

	m, err := New(Options{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()
	if got := m.Capability(); got.Mode != ModeHold {
		t.Fatalf("Capability = %+v, want hold against a live display", got)
	}

	transitions := make(chan bool, 8)
	if err := m.Watch("KeyA", func(held bool) { transitions <- held }); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	press := func(action string) {
		t.Helper()
		if out, err := exec.Command(xdotool, action, "a").CombinedOutput(); err != nil {
			t.Fatalf("xdotool %s a: %v: %s", action, err, out)
		}
	}
	expect := func(want bool) {
		t.Helper()
		select {
		case got := <-transitions:
			if got != want {
				t.Fatalf("transition = %v, want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no transition to %v within 5 s", want)
		}
	}

	press("keydown")
	expect(true)
	press("keyup")
	expect(false)
}
