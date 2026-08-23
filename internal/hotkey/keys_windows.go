//go:build windows

package hotkey

// Windows virtual-key codes (winuser.h VK_*).
//
// The left/right modifiers use the distinguishing codes (VK_LCONTROL and
// friends) rather than the merged VK_CONTROL: GetAsyncKeyState honours them,
// and a push-to-talk bound to the right Alt must not fire from the left one.
//
// The OEM punctuation codes describe positions on the US layout. That matches
// KeyboardEvent.code, which is defined the same way, so a key that reports as
// "Semicolon" in the browser is VK_OEM_1 here whatever the active layout
// prints on it.
var windowsKeys = map[string]keyCode{
	"KeyA": 0x41, "KeyB": 0x42, "KeyC": 0x43, "KeyD": 0x44, "KeyE": 0x45,
	"KeyF": 0x46, "KeyG": 0x47, "KeyH": 0x48, "KeyI": 0x49, "KeyJ": 0x4A,
	"KeyK": 0x4B, "KeyL": 0x4C, "KeyM": 0x4D, "KeyN": 0x4E, "KeyO": 0x4F,
	"KeyP": 0x50, "KeyQ": 0x51, "KeyR": 0x52, "KeyS": 0x53, "KeyT": 0x54,
	"KeyU": 0x55, "KeyV": 0x56, "KeyW": 0x57, "KeyX": 0x58, "KeyY": 0x59,
	"KeyZ": 0x5A,

	"Digit0": 0x30, "Digit1": 0x31, "Digit2": 0x32, "Digit3": 0x33,
	"Digit4": 0x34, "Digit5": 0x35, "Digit6": 0x36, "Digit7": 0x37,
	"Digit8": 0x38, "Digit9": 0x39,

	"F1": 0x70, "F2": 0x71, "F3": 0x72, "F4": 0x73, "F5": 0x74, "F6": 0x75,
	"F7": 0x76, "F8": 0x77, "F9": 0x78, "F10": 0x79, "F11": 0x7A, "F12": 0x7B,
	"F13": 0x7C, "F14": 0x7D, "F15": 0x7E, "F16": 0x7F, "F17": 0x80,
	"F18": 0x81, "F19": 0x82, "F20": 0x83, "F21": 0x84, "F22": 0x85,
	"F23": 0x86, "F24": 0x87,

	"Space":     0x20, // VK_SPACE
	"Tab":       0x09, // VK_TAB
	"Escape":    0x1B, // VK_ESCAPE
	"Enter":     0x0D, // VK_RETURN
	"Backspace": 0x08, // VK_BACK
	"CapsLock":  0x14, // VK_CAPITAL

	"Backquote":    0xC0, // VK_OEM_3
	"Minus":        0xBD, // VK_OEM_MINUS
	"Equal":        0xBB, // VK_OEM_PLUS
	"BracketLeft":  0xDB, // VK_OEM_4
	"BracketRight": 0xDD, // VK_OEM_6
	"Backslash":    0xDC, // VK_OEM_5
	"Semicolon":    0xBA, // VK_OEM_1
	"Quote":        0xDE, // VK_OEM_7
	"Comma":        0xBC, // VK_OEM_COMMA
	"Period":       0xBE, // VK_OEM_PERIOD
	"Slash":        0xBF, // VK_OEM_2

	"Insert":   0x2D, // VK_INSERT
	"Delete":   0x2E, // VK_DELETE
	"Home":     0x24, // VK_HOME
	"End":      0x23, // VK_END
	"PageUp":   0x21, // VK_PRIOR
	"PageDown": 0x22, // VK_NEXT

	"ArrowUp": 0x26, "ArrowDown": 0x28, "ArrowLeft": 0x25, "ArrowRight": 0x27,

	"ControlLeft": 0xA2, "ControlRight": 0xA3, // VK_LCONTROL / VK_RCONTROL
	"ShiftLeft": 0xA0, "ShiftRight": 0xA1, // VK_LSHIFT / VK_RSHIFT
	"AltLeft": 0xA4, "AltRight": 0xA5, // VK_LMENU / VK_RMENU
	"MetaLeft": 0x5B, "MetaRight": 0x5C, // VK_LWIN / VK_RWIN

	"Numpad0": 0x60, "Numpad1": 0x61, "Numpad2": 0x62, "Numpad3": 0x63,
	"Numpad4": 0x64, "Numpad5": 0x65, "Numpad6": 0x66, "Numpad7": 0x67,
	"Numpad8": 0x68, "Numpad9": 0x69,

	"NumpadMultiply": 0x6A, // VK_MULTIPLY
	"NumpadAdd":      0x6B, // VK_ADD
	"NumpadSubtract": 0x6D, // VK_SUBTRACT
	"NumpadDecimal":  0x6E, // VK_DECIMAL
	"NumpadDivide":   0x6F, // VK_DIVIDE
}

// windowsUnmapped names what Windows cannot address. The keypad Enter shares
// VK_RETURN with the main Enter - the extended-key flag that tells them apart
// lives on the keyboard *message*, and GetAsyncKeyState reports state, not
// messages - so binding it would fire from either key.
var windowsUnmapped = map[string]string{
	"NumpadEnter": "VK_RETURN does not distinguish the keypad Enter from the main one",
}

// platformTable exposes this platform's mapping to the shared table test.
func platformTable() (mapped map[string]keyCode, unmapped map[string]string) {
	return windowsKeys, windowsUnmapped
}
