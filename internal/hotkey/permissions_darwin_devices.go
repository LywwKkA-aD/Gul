//go:build darwin && cgo && devices

package hotkey

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework ApplicationServices

#include <ApplicationServices/ApplicationServices.h>

static int hkAXTrusted(void)       { return AXIsProcessTrusted() ? 1 : 0; }
static int hkCanListenEvents(void) { return CGPreflightListenEventAccess() ? 1 : 0; }
static int hkCanPostEvents(void)   { return CGPreflightPostEventAccess() ? 1 : 0; }

// hkSecondsSinceKeyDown is the independent witness: it reads the same event
// source as CGEventSourceKeyState, so a value that keeps up with a real
// keyboard proves that source is reporting live hardware to this process.
static double hkSecondsSinceKeyDown(void) {
    return CGEventSourceSecondsSinceLastEventType(
        kCGEventSourceStateCombinedSessionState, kCGEventKeyDown);
}
*/
import "C"

import "time"

// The macOS privacy probes used by the manual measurement in
// permissions_darwin_devices_test.go. cgo is not allowed inside _test.go
// files, so the calls live here, behind the same devices tag, and never reach
// a shipped build.

// darwinPermissions reports what the privacy machinery grants this process.
func darwinPermissions() (trusted, canListen, canPost bool) {
	return C.hkAXTrusted() != 0, C.hkCanListenEvents() != 0, C.hkCanPostEvents() != 0
}

// secondsSinceKeyDown is how long ago the combined session state last saw a
// key go down.
func secondsSinceKeyDown() time.Duration {
	return time.Duration(float64(C.hkSecondsSinceKeyDown()) * float64(time.Second))
}
