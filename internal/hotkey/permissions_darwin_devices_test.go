//go:build darwin && cgo && devices

package hotkey

import (
	"testing"
	"time"
)

// Records what macOS grants this process, then shows whether key state is
// readable anyway.
//
//	go test -tags devices -run TestLiveDarwinKeyStateNeedsNoPermission -v ./internal/hotkey
//
// Measured on macOS 26.5.2 (build 25F84): all three grants absent, and real
// key presses still observed. Should a future release move
// CGEventSourceKeyState behind Input Monitoring, this is where it surfaces
// first, and the package documentation and Capability.Reason then have to say
// so.
func TestLiveDarwinKeyStateNeedsNoPermission(t *testing.T) {
	trusted, canListen, canPost := darwinPermissions()
	t.Logf("AXIsProcessTrusted=%v CGPreflightListenEventAccess=%v CGPreflightPostEventAccess=%v",
		trusted, canListen, canPost)

	if trusted || canListen {
		t.Log("this process already holds Accessibility or Input Monitoring: revoke them " +
			"for the terminal running the test to make the measurement meaningful")
	}

	t.Log("press and release any key within 20 s")
	before := secondsSinceKeyDown()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if secondsSinceKeyDown() < before {
			t.Logf("key-down activity observed with trusted=%v listen=%v", trusted, canListen)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no key-down reached the combined session state within 20 s: " +
		"either nothing was pressed, or this source is now gated")
}
