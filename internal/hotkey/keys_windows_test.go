//go:build windows

package hotkey

import "testing"

func TestWindowsCodesMatchWinuser(t *testing.T) {
	// Anchors transcribed from winuser.h.
	for name, want := range map[string]keyCode{
		"KeyA":         0x41, // 'A'
		"KeyZ":         0x5A, // 'Z'
		"Digit0":       0x30, // '0'
		"Digit9":       0x39, // '9'
		"Space":        0x20, // VK_SPACE
		"Tab":          0x09, // VK_TAB
		"Enter":        0x0D, // VK_RETURN
		"Backspace":    0x08, // VK_BACK
		"Escape":       0x1B, // VK_ESCAPE
		"CapsLock":     0x14, // VK_CAPITAL
		"Backquote":    0xC0, // VK_OEM_3
		"Insert":       0x2D, // VK_INSERT
		"Delete":       0x2E, // VK_DELETE
		"PageUp":       0x21, // VK_PRIOR
		"PageDown":     0x22, // VK_NEXT
		"ArrowLeft":    0x25, // VK_LEFT
		"ArrowDown":    0x28, // VK_DOWN
		"F1":           0x70, // VK_F1
		"F24":          0x87, // VK_F24
		"ControlLeft":  0xA2, // VK_LCONTROL
		"ControlRight": 0xA3, // VK_RCONTROL
		"ShiftLeft":    0xA0, // VK_LSHIFT
		"AltRight":     0xA5, // VK_RMENU
		"MetaLeft":     0x5B, // VK_LWIN
		"Numpad0":      0x60, // VK_NUMPAD0
		"NumpadDivide": 0x6F, // VK_DIVIDE
	} {
		if got := windowsKeys[name]; got != want {
			t.Errorf("windowsKeys[%q] = %#x, want %#x", name, got, want)
		}
	}
}

func TestWindowsUsesTheSidedModifierCodes(t *testing.T) {
	// VK_CONTROL (0x10..0x12) merges both sides; a push-to-talk on the right
	// Alt must not fire from the left one.
	for _, name := range []string{"ControlLeft", "ControlRight", "ShiftLeft", "ShiftRight", "AltLeft", "AltRight"} {
		if code := windowsKeys[name]; code < 0xA0 || code > 0xA5 {
			t.Errorf("windowsKeys[%q] = %#x, want a sided VK in 0xA0..0xA5", name, code)
		}
	}
}

func TestWindowsCannotAddressTheKeypadEnter(t *testing.T) {
	// It shares VK_RETURN with the main Enter, so a binding would fire from
	// both. Declaring it unmappable turns that into a precise error instead
	// of a key that quietly does the wrong thing.
	if _, mapped := windowsKeys["NumpadEnter"]; mapped {
		t.Error("NumpadEnter has a Windows code, but VK_RETURN cannot tell the two Enters apart")
	}
	if len(windowsUnmapped) != 1 {
		t.Fatalf("windowsUnmapped = %v, want only NumpadEnter", windowsUnmapped)
	}
	if _, declared := windowsUnmapped["NumpadEnter"]; !declared {
		t.Fatalf("windowsUnmapped = %v, want NumpadEnter declared", windowsUnmapped)
	}
}
