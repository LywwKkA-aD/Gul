package hotkey

import "testing"

func TestWaylandDetectionFollowsWails(t *testing.T) {
	// The rule has to match pkg/application/global_shortcut_linux.go: both
	// halves must agree on which backend is in play before the user interface
	// can explain it.
	for _, tc := range []struct {
		name          string
		sessionType   string
		waylandSocket string
		want          bool
	}{
		{"declared wayland", "wayland", "", true},
		{"declared wayland, mixed case", "Wayland", "", true},
		{"declared x11 wins over a stray socket", "x11", "wayland-0", false},
		{"declared x11, mixed case", "X11", "wayland-0", false},
		{"undeclared, socket present", "", "wayland-0", true},
		{"undeclared, no socket", "", "", false},
		{"tty session with a socket", "tty", "wayland-1", true},
		{"tty session without a socket", "tty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_SESSION_TYPE", tc.sessionType)
			t.Setenv("WAYLAND_DISPLAY", tc.waylandSocket)
			if got := isWaylandSession(); got != tc.want {
				t.Fatalf("isWaylandSession() = %v, want %v", got, tc.want)
			}
		})
	}
}
