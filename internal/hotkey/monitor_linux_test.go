//go:build linux

package hotkey

import (
	"errors"
	"testing"
)

func TestLinuxWaylandFallsBackToToggle(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")

	reg := newFakeRegistrar()
	m, err := New(Options{Registrar: reg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()

	capability := m.Capability()
	if capability.Mode != ModeToggle {
		t.Fatalf("Mode = %v, want ModeToggle: a compositor never hands out key state", capability.Mode)
	}
	if !hasCyrillic(capability.Reason) {
		t.Fatalf("Reason = %q, want Russian text for the user", capability.Reason)
	}

	rec := newRecorder()
	if err := m.Watch("KeyV", rec.record); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	reg.activate(t, "V")
	reg.activate(t, "V")
	rec.waitFor(t, 2)
	rec.want(t, true, false)
}

func TestLinuxWaylandWithoutAPortalIsUnsupported(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")

	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()

	// A monitor that could never fire must say so rather than accept a Watch
	// and stay silent.
	if got := m.Capability(); got.Mode != ModeUnsupported || got.Reason != waylandNoPortalReason {
		t.Fatalf("Capability = %+v, want unsupported with the no-portal reason", got)
	}
	if err := m.Watch("Space", func(bool) {}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Watch = %v, want ErrUnsupported", err)
	}
}

func TestLinuxX11SessionIsEitherUsableOrExplained(t *testing.T) {
	// A developer machine has a display and reports hold; headless CI does
	// not and must explain itself. Both are correct; silence is not.
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")

	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()

	capability := m.Capability()
	switch capability.Mode {
	case ModeHold:
		if capability.Reason != "" {
			t.Fatalf("hold mode carries a reason it does not need: %q", capability.Reason)
		}
	default:
		if !hasCyrillic(capability.Reason) {
			t.Fatalf("mode %v carries no Russian reason: %q", capability.Mode, capability.Reason)
		}
	}
}
