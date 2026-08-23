//go:build darwin && cgo

package hotkey

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework CoreGraphics

#include <CoreGraphics/CoreGraphics.h>

// hkKeyDown reads one key from the combined session state, which merges the
// HID stream with events posted into the session. That is the source that
// reflects what the user's fingers are doing regardless of which application
// is in front.
static int hkKeyDown(unsigned short code) {
    return CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState, (CGKeyCode)code) ? 1 : 0;
}
*/
import "C"

// darwinSource polls CGEventSourceKeyState.
//
// No permission is involved. Measured on macOS 26.5.2 (build 25F84) from a
// process with AXIsProcessTrusted false, CGPreflightListenEventAccess false
// and CGPreflightPostEventAccess false: real physical key presses were still
// observed through this source. TCC gates event taps and direct HID device
// access, both of which read the event *stream*; this call reads state, and is
// not on that list. Hold-to-talk therefore needs neither a prompt nor an
// entitlement, and the live check in permissions_darwin_devices_test.go re-runs
// the measurement on a real machine.
//
// There is nothing to open or close: the call is stateless and thread safe.
type darwinSource struct{}

func (darwinSource) lookup(name string) (keyCode, bool) {
	code, ok := darwinKeys[name]
	return code, ok
}

func (darwinSource) open() error { return nil }

func (darwinSource) close() {}

func (darwinSource) pressed(code keyCode) (bool, error) {
	return C.hkKeyDown(C.ushort(code)) != 0, nil
}

func newMonitor(opts Options) (Monitor, error) {
	return newPoller(darwinSource{}, opts.PollInterval, Capability{Mode: ModeHold}), nil
}
