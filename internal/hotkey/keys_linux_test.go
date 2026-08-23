//go:build linux

package hotkey

import "testing"

func TestLinuxCodesMatchEvdev(t *testing.T) {
	// Anchors transcribed from linux/input-event-codes.h.
	for name, want := range map[string]keyCode{
		"KeyA":         30,  // KEY_A
		"KeyQ":         16,  // KEY_Q
		"KeyZ":         44,  // KEY_Z
		"Digit1":       2,   // KEY_1
		"Digit0":       11,  // KEY_0, after the nines
		"Space":        57,  // KEY_SPACE
		"Escape":       1,   // KEY_ESC
		"Enter":        28,  // KEY_ENTER
		"Backspace":    14,  // KEY_BACKSPACE
		"CapsLock":     58,  // KEY_CAPSLOCK
		"Backquote":    41,  // KEY_GRAVE
		"Insert":       110, // KEY_INSERT
		"Delete":       111, // KEY_DELETE
		"ArrowUp":      103, // KEY_UP
		"F1":           59,  // KEY_F1
		"F12":          88,  // KEY_F12, not adjacent to F11's neighbours
		"F13":          183, // KEY_F13, the second F block
		"F24":          194, // KEY_F24
		"ControlLeft":  29,  // KEY_LEFTCTRL
		"ControlRight": 97,  // KEY_RIGHTCTRL
		"MetaLeft":     125, // KEY_LEFTMETA
		"Numpad0":      82,  // KEY_KP0
		"Numpad7":      71,  // KEY_KP7
		"NumpadDivide": 98,  // KEY_KPSLASH
	} {
		if got := evdevKeys[name]; got != want {
			t.Errorf("evdevKeys[%q] = %d, want %d", name, got, want)
		}
	}
}

func TestLinuxKeycodesAreEvdevPlusEight(t *testing.T) {
	// The classic X11 keycodes are the check that the offset is right: a=38,
	// q=24, z=52 have been those numbers for as long as XKB has existed.
	for name, want := range map[string]keyCode{
		"KeyA":  38,
		"KeyQ":  24,
		"KeyZ":  52,
		"Space": 65,
		"F1":    67,
	} {
		if got := linuxKeys[name]; got != want {
			t.Errorf("linuxKeys[%q] = %d, want %d", name, got, want)
		}
	}
	for name, evdev := range evdevKeys {
		if got := linuxKeys[name]; got != evdev+evdevToX11Offset {
			t.Errorf("linuxKeys[%q] = %d, want %d", name, got, evdev+evdevToX11Offset)
		}
	}
}

func TestLinuxKeycodesFitTheKeymapVector(t *testing.T) {
	// XQueryKeymap reports 32 bytes, so a keycode above 255 could not be
	// tested at all.
	for name, code := range linuxKeys {
		if code > 255 {
			t.Errorf("linuxKeys[%q] = %d, outside the 256-bit keymap vector", name, code)
		}
	}
}

func TestLinuxMapsTheWholeVocabulary(t *testing.T) {
	if len(linuxUnmapped) != 0 {
		t.Fatalf("linuxUnmapped = %v, want empty", linuxUnmapped)
	}
}
