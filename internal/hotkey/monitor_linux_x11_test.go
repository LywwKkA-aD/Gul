//go:build linux && cgo && !server

package hotkey

import (
	"strings"
	"testing"
)

func TestLinuxUnreachableDisplayIsReportedToTheUser(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":99999")

	m, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Stop()

	capability := m.Capability()
	if capability.Mode != ModeUnsupported {
		t.Fatalf("Mode = %v, want ModeUnsupported for a display that cannot be opened", capability.Mode)
	}
	if !strings.HasPrefix(capability.Reason, "Нет доступа к состоянию клавиш") {
		t.Fatalf("Reason = %q, want it to open with the Russian explanation", capability.Reason)
	}
	if !strings.Contains(capability.Reason, ":99999") {
		t.Fatalf("Reason = %q, want the unreachable display named for diagnosis", capability.Reason)
	}
}
