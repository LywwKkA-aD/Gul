//go:build windows

package hotkey

import "syscall"

// windowsSource polls GetAsyncKeyState.
//
// No permission is involved and no window needs focus: the call reports the
// physical state of the key at the moment it is made. It is scoped to the
// current desktop, so a key held while the secure desktop (the UAC prompt) is
// up reads as released - the transmission closes, which is the safe direction.
//
// user32.dll is on the KnownDLLs list, so the loader always resolves it from
// the system directory; the DLL-preloading hazard that makes NewLazyDLL a poor
// default does not reach it. Using it keeps this file on the standard library
// and leaves go.mod alone.
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

type windowsSource struct{}

func (windowsSource) lookup(name string) (keyCode, bool) {
	code, ok := windowsKeys[name]
	return code, ok
}

func (windowsSource) open() error { return nil }

func (windowsSource) close() {}

func (windowsSource) pressed(code keyCode) (bool, error) {
	// Bit 15 is "currently down". Bit 0, "pressed since the last call", is
	// deliberately ignored: reading it consumes it, so two callers polling the
	// same key would steal presses from one another.
	state, _, _ := procGetAsyncKeyState.Call(uintptr(code))
	return state&0x8000 != 0, nil
}

func newMonitor(opts Options) (Monitor, error) {
	return newPoller(windowsSource{}, opts.PollInterval, Capability{Mode: ModeHold}), nil
}
