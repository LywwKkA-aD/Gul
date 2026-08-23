package hotkey

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// hasCyrillic reports whether s carries Russian text. Capability.Reason is the
// one string this package hands to the user interface, and the interface
// renders it without translating.
func hasCyrillic(s string) bool {
	for _, r := range s {
		if r >= 0x0400 && r <= 0x04FF {
			return true
		}
	}
	return false
}

func TestNewRejectsANegativeInterval(t *testing.T) {
	if _, err := New(Options{PollInterval: -time.Millisecond}); err == nil {
		t.Fatal("New accepted a negative PollInterval")
	}
}

func TestNewFillsInTheDefaultInterval(t *testing.T) {
	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p, ok := m.(*poller)
	if !ok {
		t.Skipf("%s has no polling backend", m.Capability().Mode)
	}
	if p.interval != DefaultPollInterval {
		t.Fatalf("interval = %s, want %s", p.interval, DefaultPollInterval)
	}
}

func TestNewClampsAnIntervalThatWouldSpin(t *testing.T) {
	m, err := New(Options{PollInterval: time.Microsecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p, ok := m.(*poller)
	if !ok {
		t.Skipf("%s has no polling backend", m.Capability().Mode)
	}
	// A configuration file must not be able to turn the poller into a spin.
	if p.interval != MinPollInterval {
		t.Fatalf("interval = %s, want it raised to %s", p.interval, MinPollInterval)
	}
}

func TestNewKeepsAnExplicitInterval(t *testing.T) {
	m, err := New(Options{PollInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p, ok := m.(*poller)
	if !ok {
		t.Skipf("%s has no polling backend", m.Capability().Mode)
	}
	if p.interval != 50*time.Millisecond {
		t.Fatalf("interval = %s, want 50ms", p.interval)
	}
}

func TestNewAlwaysReturnsAMonitor(t *testing.T) {
	// A platform that cannot watch keys is not an error: the caller needs a
	// Capability to show either way.
	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("New returned a nil Monitor with no error")
	}
	defer m.Stop()

	capability := m.Capability()
	switch capability.Mode {
	case ModeHold:
		if capability.Reason != "" {
			if !hasCyrillic(capability.Reason) {
				t.Fatalf("Reason = %q, want Russian text", capability.Reason)
			}
		}
	case ModeToggle, ModeUnsupported:
		if capability.Reason == "" {
			t.Fatalf("mode %v carries no reason for the user", capability.Mode)
		}
		if !hasCyrillic(capability.Reason) {
			t.Fatalf("Reason = %q, want Russian text", capability.Reason)
		}
	default:
		t.Fatalf("unexpected mode %v", capability.Mode)
	}
}

func TestNewMonitorRejectsAnUnknownKey(t *testing.T) {
	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()
	// Every backend, including the unsupported one, has to tell a misspelled
	// key apart from a platform limit.
	if err := m.Watch("Spacebar", func(bool) {}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Watch = %v, want ErrUnknownKey", err)
	}
}

func TestRealMonitorWatchesAKeyEndToEnd(t *testing.T) {
	m, err := New(Options{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()
	if m.Capability().Mode != ModeHold {
		t.Skipf("no key state on this machine: %s", m.Capability().Reason)
	}

	transitions := make(chan bool, 8)
	// F13 is on no laptop keyboard and is not a key a test machine holds
	// down, so the run must stay silent while the real backend polls it.
	if err := m.Watch("F13", func(held bool) { transitions <- held }); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	m.Stop()

	select {
	case held := <-transitions:
		t.Fatalf("an unpressed key reported held = %v", held)
	default:
	}
}

func TestRealMonitorStopIsIdempotent(t *testing.T) {
	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.Stop()
	m.Stop()
}

func TestModeString(t *testing.T) {
	for mode, want := range map[Mode]string{
		ModeUnsupported: "unsupported",
		ModeHold:        "hold",
		ModeToggle:      "toggle",
		Mode(9):         "Mode(9)",
	} {
		if got := mode.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", int(mode), got, want)
		}
	}
}

func TestUnsupportedMonitorExplainsItselfWithoutStarting(t *testing.T) {
	const reason = "Глобальная клавиша недоступна на этой платформе."
	m := newUnsupported(reason)

	if got := m.Capability(); got.Mode != ModeUnsupported || got.Reason != reason {
		t.Fatalf("Capability = %+v, want unsupported with the given reason", got)
	}
	err := m.Watch("Space", func(bool) {})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Watch = %v, want ErrUnsupported", err)
	}
	// The error text is for the log; the Russian belongs to Reason.
	if hasCyrillic(err.Error()) {
		t.Fatalf("Watch error %q carries user-facing text", err)
	}
	if err := m.Watch("Spacebar", func(bool) {}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Watch on a bad name = %v, want ErrUnknownKey", err)
	}
	if err := m.Watch("Space", nil); err == nil {
		t.Fatal("Watch accepted a nil callback")
	}
	m.Stop()
	m.Stop()
}

func TestErrorsAreDistinguishable(t *testing.T) {
	// Phase two branches on these, so they must not collapse into each other.
	for _, pair := range [][2]error{
		{ErrUnknownKey, ErrKeyUnavailable},
		{ErrUnknownKey, ErrUnsupported},
		{ErrKeyUnavailable, ErrUnsupported},
	} {
		if errors.Is(pair[0], pair[1]) || errors.Is(pair[1], pair[0]) {
			t.Errorf("%v and %v are not distinguishable", pair[0], pair[1])
		}
	}
	for _, err := range []error{ErrUnknownKey, ErrKeyUnavailable, ErrUnsupported} {
		if !strings.HasPrefix(err.Error(), "hotkey: ") {
			t.Errorf("%v is not tagged with the package name", err)
		}
	}
}
