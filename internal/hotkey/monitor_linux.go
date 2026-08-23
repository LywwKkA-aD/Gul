//go:build linux && cgo && !server

package hotkey

/*
#cgo pkg-config: x11

#include <X11/Xlib.h>

static Display *hkOpenDisplay(void) {
    return XOpenDisplay(NULL);
}

static void hkCloseDisplay(Display *d) {
    if (d != NULL) {
        XCloseDisplay(d);
    }
}

// hkKeyDown tests one keycode in the server's 256-bit keymap vector. Any X
// client may ask for it; nothing here needs a grab, a focused window or a
// privilege.
static int hkKeyDown(Display *d, unsigned int keycode) {
    if (d == NULL || keycode > 255) {
        return 0;
    }
    unsigned char keys[32];
    XQueryKeymap(d, (char *)keys);
    return (keys[keycode >> 3] & (1u << (keycode & 7))) != 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
)

// x11Source polls XQueryKeymap over a display connection of its own.
//
// The connection is separate from the one the window uses so that a poll never
// interleaves with Wails' own Xlib traffic. It is opened and closed on the
// poll goroutine, which is pinned to an OS thread for the lifetime of the
// watch, which is the threading discipline Xlib expects without XInitThreads.
//
// One hazard is worth stating plainly: should the X server go away, Xlib's
// default I/O error handler terminates the process, and a handler that returns
// is undefined behaviour, so it cannot be caught. This connection shares that
// fate with the window's own connection - if the server dies, the application
// was going down anyway.
type x11Source struct {
	display *C.Display
}

func (s *x11Source) lookup(name string) (keyCode, bool) {
	code, ok := linuxKeys[name]
	return code, ok
}

func (s *x11Source) open() error {
	display := C.hkOpenDisplay()
	if display == nil {
		return fmt.Errorf("XOpenDisplay failed (DISPLAY=%q)", os.Getenv("DISPLAY"))
	}
	s.display = display
	return nil
}

func (s *x11Source) close() {
	if s.display != nil {
		C.hkCloseDisplay(s.display)
		s.display = nil
	}
}

func (s *x11Source) pressed(code keyCode) (bool, error) {
	if s.display == nil {
		return false, errors.New("x11: display connection is closed")
	}
	return C.hkKeyDown(s.display, C.uint(code)) != 0, nil
}

func newMonitor(opts Options) (Monitor, error) {
	if isWaylandSession() {
		return newToggleMonitor(opts.Registrar, waylandToggleReason), nil
	}
	// Probe now rather than at the first Watch: the capability is what the
	// settings screen renders, and it has to be truthful before the user
	// picks a key.
	probe := &x11Source{}
	if err := probe.open(); err != nil {
		return newUnsupported("Нет доступа к состоянию клавиш: " + err.Error()), nil
	}
	probe.close()
	return newPoller(&x11Source{}, opts.PollInterval, Capability{Mode: ModeHold}), nil
}
