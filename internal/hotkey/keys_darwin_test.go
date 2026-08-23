//go:build darwin

package hotkey

import "testing"

func TestDarwinCodesMatchCarbon(t *testing.T) {
	// Anchors transcribed from Carbon HIToolbox Events.h. MetaLeft is the one
	// that was also confirmed against a live machine: a physical Command press
	// read back as 0x37 through CGEventSourceKeyState.
	for name, want := range map[string]keyCode{
		"KeyA":        0x00, // kVK_ANSI_A
		"KeyZ":        0x06, // kVK_ANSI_Z
		"KeyQ":        0x0C, // kVK_ANSI_Q
		"Digit0":      0x1D, // kVK_ANSI_0
		"Digit5":      0x17, // kVK_ANSI_5, out of numeric order on purpose
		"Digit6":      0x16, // kVK_ANSI_6
		"Space":       0x31, // kVK_Space
		"Enter":       0x24, // kVK_Return
		"Backspace":   0x33, // kVK_Delete, the key above Return
		"Delete":      0x75, // kVK_ForwardDelete
		"Insert":      0x72, // kVK_Help, where a PC Insert lands
		"Escape":      0x35, // kVK_Escape
		"CapsLock":    0x39, // kVK_CapsLock
		"Backquote":   0x32, // kVK_ANSI_Grave
		"MetaLeft":    0x37, // kVK_Command
		"ShiftLeft":   0x38, // kVK_Shift
		"ControlLeft": 0x3B, // kVK_Control
		"AltRight":    0x3D, // kVK_RightOption
		"F1":          0x7A, // kVK_F1
		"F13":         0x69, // kVK_F13
		"F20":         0x5A, // kVK_F20
		"ArrowLeft":   0x7B, // kVK_LeftArrow
		"Numpad0":     0x52, // kVK_ANSI_Keypad0
	} {
		if got := darwinKeys[name]; got != want {
			t.Errorf("darwinKeys[%q] = %#x, want %#x", name, got, want)
		}
	}
}

func TestDarwinDeclaresTheFunctionKeysCarbonLacks(t *testing.T) {
	// Carbon stops at F20; F21 through F24 have no virtual key code, and
	// guessing one would bind an unrelated physical key.
	for _, name := range []string{"F21", "F22", "F23", "F24"} {
		if _, mapped := darwinKeys[name]; mapped {
			t.Errorf("%q has a macOS code, but Carbon defines none", name)
		}
		if _, declared := darwinUnmapped[name]; !declared {
			t.Errorf("%q is neither mapped nor declared unmappable", name)
		}
	}
	if _, wrong := darwinUnmapped["F20"]; wrong {
		t.Error("F20 exists in Carbon and must not be declared unmappable")
	}
}
