// Package hotkey watches a single keyboard key system wide and reports it
// going down and coming back up, so push-to-talk keeps working while the
// application window sits in the background.
//
// # Why this exists next to Wails' GlobalShortcutManager
//
// That manager delivers presses only. Carbon reports kEventHotKeyPressed,
// Windows reports WM_HOTKEY, the XDG portal reports Activated and the X11
// backend reports KeyPress; none of them carries a release, and the callback
// is a bare func(). Push-to-talk needs both edges, so this package polls the
// operating system's live key state instead and falls back to a toggle only
// where no such state is reachable.
//
// # Key names
//
// Keys are named by their KeyboardEvent.code value ("Space", "KeyV",
// "ControlLeft", "F13"): a physical position, independent of the keyboard
// layout, and the same vocabulary the settings already store. A name outside
// the vocabulary is an error, never a watch that silently never fires.
//
// # Backends and permissions
//
// macOS polls CGEventSourceKeyState against kCGEventSourceStateCombinedSessionState.
// Measured on macOS 26.5.2 (build 25F84): a process holding neither
// Accessibility (AXIsProcessTrusted false) nor Input Monitoring
// (CGPreflightListenEventAccess false) still observes real physical key
// presses through that source. The TCC gate covers event taps and direct HID
// device access, not this state query, so hold-to-talk needs no permission
// prompt and no entitlement.
//
// Windows polls GetAsyncKeyState, which reports the physical key regardless of
// which window has focus and needs no permission. It sees only the current
// desktop, so a key held while the secure desktop (UAC) is up reads as
// released.
//
// Linux under X11 polls XQueryKeymap on a dedicated display connection. Any
// client may read the keymap, so no permission is involved, but the polling
// connection shares the fate of the process: should the X server go away,
// Xlib's default I/O error handler terminates the application, exactly as it
// would through the window's own connection.
//
// Linux under Wayland has no way for a client to read key state at all - by
// design, not by omission. The only sanctioned mechanism is the XDG desktop
// portal's GlobalShortcuts interface, which delivers activations without
// releases, so this package degrades to ModeToggle there: one activation opens
// the microphone, the next closes it. The compositor, not the application,
// picks the final combination, so the user interface must show what was
// actually bound rather than what was requested.
//
// Every other platform, and any build without cgo where cgo is required,
// reports ModeUnsupported. The window-focused push-to-talk in the frontend
// stays the fallback for all of those.
package hotkey

import (
	"errors"
	"fmt"
	"time"
)

// Mode is what a Monitor can actually deliver on this machine.
type Mode int

const (
	// ModeUnsupported means no global key watching is available; the caller
	// keeps its window-focused push-to-talk.
	ModeUnsupported Mode = iota
	// ModeHold reports true while the key is physically held and false once
	// it is released: real push-to-talk.
	ModeHold
	// ModeToggle reports true on one activation and false on the next,
	// because the platform hands out presses without releases.
	ModeToggle
)

// String names the mode for logs. It is not user-facing text.
func (m Mode) String() string {
	switch m {
	case ModeHold:
		return "hold"
	case ModeToggle:
		return "toggle"
	case ModeUnsupported:
		return "unsupported"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// Capability describes what the Monitor returned by New can do here.
//
// Reason is the single user-facing channel of this package: Russian, ready to
// render, and non-empty only when the mode is something other than plain
// hold-to-talk. Errors returned by the API are for logs and carry English
// text. An empty Reason on ModeHold means there is nothing to tell the user.
type Capability struct {
	Mode   Mode
	Reason string
}

// Registrar is the slice of Wails' GlobalShortcutManager the Wayland backend
// needs. Declaring it here instead of importing the manager keeps the package
// free of an application instance, which is what lets the toggle path be
// tested with a fake.
type Registrar interface {
	Register(accelerator string, cb func()) error
	Unregister(accelerator string) error
}

// Monitor watches one key at a time.
//
// Watch and Stop are safe to call from any goroutine. The onChange callback is
// not: it runs on the goroutine that observed the transition and must return
// promptly, because the next poll waits for it. It must not call Watch or Stop
// on the same Monitor - both wait for the callback to finish and would
// deadlock against themselves.
type Monitor interface {
	// Watch reports every held/released transition of key to onChange until
	// Stop. It replaces whatever was being watched before, and leaves the
	// Monitor stopped if it returns an error.
	//
	// It fails with ErrUnknownKey for a name outside the vocabulary,
	// ErrKeyUnavailable for a name this backend cannot address, and
	// ErrUnsupported when there is no key-state backend at all.
	Watch(key string, onChange func(held bool)) error

	// Stop ends the current watch and is idempotent. It never delivers a
	// callback, not even a release for a key that was still held: a caller
	// that stops mid-press owns clearing its own transmit state. Stop waits
	// for any callback already in flight, so once it returns no further
	// callback can arrive.
	Stop()

	// Capability reports what this Monitor delivers. It is fixed for the
	// lifetime of the Monitor.
	Capability() Capability
}

// Options configures New.
type Options struct {
	// Registrar registers the toggle shortcut on Wayland, where key state is
	// unreadable. Ignored - and may be nil - on every other backend. A nil
	// Registrar on Wayland yields ModeUnsupported rather than a Monitor that
	// silently never fires.
	Registrar Registrar

	// PollInterval is the gap between key-state reads. Zero selects
	// DefaultPollInterval. Values below MinPollInterval are raised to it:
	// polling faster burns CPU without shortening the delay a talker can
	// hear, and a config file must not be able to spin the poller.
	// The value is ignored in ModeToggle, which is event driven.
	PollInterval time.Duration
}

const (
	// DefaultPollInterval keeps the press-to-transmit delay inside two 10 ms
	// audio frames while costing 50 state reads per second.
	DefaultPollInterval = 20 * time.Millisecond

	// MinPollInterval is the floor a caller cannot go under.
	MinPollInterval = 5 * time.Millisecond
)

// Errors reported by Watch. Callers distinguish them with errors.Is; the text
// is English because these travel to the log, not to the user, for which
// Capability.Reason exists.
var (
	// ErrUnknownKey means the name is not a KeyboardEvent.code this package
	// knows.
	ErrUnknownKey = errors.New("hotkey: unknown key name")

	// ErrKeyUnavailable means the name is valid but this platform has no
	// code for it, such as F21 through F24 on macOS.
	ErrKeyUnavailable = errors.New("hotkey: key is not addressable on this platform")

	// ErrUnsupported means this build has no way to read global key state.
	ErrUnsupported = errors.New("hotkey: global key state is unavailable")
)

// New builds the Monitor for the current platform and session.
//
// A platform that cannot watch keys is not an error: New returns a Monitor
// whose Capability explains the situation in Russian and whose Watch fails
// with ErrUnsupported, so the caller always has something to show the user. An
// error means the Options themselves were rejected.
func New(opts Options) (Monitor, error) {
	if opts.PollInterval < 0 {
		return nil, fmt.Errorf("hotkey: PollInterval must not be negative, got %s", opts.PollInterval)
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.PollInterval < MinPollInterval {
		opts.PollInterval = MinPollInterval
	}
	return newMonitor(opts)
}
