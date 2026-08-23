package hotkey

import (
	"os"
	"strings"
)

// isWaylandSession reports whether this process runs under Wayland, using the
// same rule as Wails' own backend selection (pkg/application/global_shortcut_linux.go):
// XDG_SESSION_TYPE decides when it is set, and WAYLAND_DISPLAY breaks the tie
// otherwise. Matching Wails matters because both halves have to agree on which
// backend is in play before the user interface can explain it.
//
// It is not guarded by a build tag so that the rule stays under test on every
// platform, not only on the one where it is consulted.
func isWaylandSession() bool {
	switch strings.ToLower(os.Getenv("XDG_SESSION_TYPE")) {
	case "wayland":
		return true
	case "x11":
		return false
	}
	return os.Getenv("WAYLAND_DISPLAY") != ""
}
