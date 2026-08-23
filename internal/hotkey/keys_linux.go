//go:build linux

package hotkey

// Linux key codes.
//
// The table holds evdev codes (linux/input-event-codes.h KEY_*), and the X11
// keycode is that code plus 8. Going through evdev rather than through keysyms
// is what makes the mapping layout independent: XKeysymToKeycode would resolve
// "a" to whatever key currently produces an "a", so a user on a Cyrillic
// layout would find their push-to-talk key had moved. KeyboardEvent.code names
// a physical position, and evdev numbers the same positions.
//
// The offset of 8 is the XKB convention every modern X server follows, and it
// is also how the browser engine derives the KeyboardEvent.code values the
// settings store, so both halves agree by construction.
const evdevToX11Offset = 8

var linuxKeys = buildLinuxKeys()

func buildLinuxKeys() map[string]keyCode {
	table := make(map[string]keyCode, len(evdevKeys))
	for name, code := range evdevKeys {
		table[name] = code + evdevToX11Offset
	}
	return table
}

var evdevKeys = map[string]keyCode{
	"KeyA": 30, "KeyB": 48, "KeyC": 46, "KeyD": 32, "KeyE": 18,
	"KeyF": 33, "KeyG": 34, "KeyH": 35, "KeyI": 23, "KeyJ": 36,
	"KeyK": 37, "KeyL": 38, "KeyM": 50, "KeyN": 49, "KeyO": 24,
	"KeyP": 25, "KeyQ": 16, "KeyR": 19, "KeyS": 31, "KeyT": 20,
	"KeyU": 22, "KeyV": 47, "KeyW": 17, "KeyX": 45, "KeyY": 21,
	"KeyZ": 44,

	"Digit0": 11, "Digit1": 2, "Digit2": 3, "Digit3": 4, "Digit4": 5,
	"Digit5": 6, "Digit6": 7, "Digit7": 8, "Digit8": 9, "Digit9": 10,

	"F1": 59, "F2": 60, "F3": 61, "F4": 62, "F5": 63, "F6": 64,
	"F7": 65, "F8": 66, "F9": 67, "F10": 68, "F11": 87, "F12": 88,
	"F13": 183, "F14": 184, "F15": 185, "F16": 186, "F17": 187,
	"F18": 188, "F19": 189, "F20": 190, "F21": 191, "F22": 192,
	"F23": 193, "F24": 194,

	"Space":     57,
	"Tab":       15,
	"Escape":    1,
	"Enter":     28,
	"Backspace": 14,
	"CapsLock":  58,

	"Backquote":    41, // KEY_GRAVE
	"Minus":        12, // KEY_MINUS
	"Equal":        13, // KEY_EQUAL
	"BracketLeft":  26, // KEY_LEFTBRACE
	"BracketRight": 27, // KEY_RIGHTBRACE
	"Backslash":    43, // KEY_BACKSLASH
	"Semicolon":    39, // KEY_SEMICOLON
	"Quote":        40, // KEY_APOSTROPHE
	"Comma":        51, // KEY_COMMA
	"Period":       52, // KEY_DOT
	"Slash":        53, // KEY_SLASH

	"Insert":   110,
	"Delete":   111,
	"Home":     102,
	"End":      107,
	"PageUp":   104,
	"PageDown": 109,

	"ArrowUp": 103, "ArrowDown": 108, "ArrowLeft": 105, "ArrowRight": 106,

	"ControlLeft": 29, "ControlRight": 97,
	"ShiftLeft": 42, "ShiftRight": 54,
	"AltLeft": 56, "AltRight": 100,
	"MetaLeft": 125, "MetaRight": 126,

	"Numpad0": 82, "Numpad1": 79, "Numpad2": 80, "Numpad3": 81,
	"Numpad4": 75, "Numpad5": 76, "Numpad6": 77, "Numpad7": 71,
	"Numpad8": 72, "Numpad9": 73,

	"NumpadMultiply": 55, // KEY_KPASTERISK
	"NumpadAdd":      78, // KEY_KPPLUS
	"NumpadSubtract": 74, // KEY_KPMINUS
	"NumpadDecimal":  83, // KEY_KPDOT
	"NumpadDivide":   98, // KEY_KPSLASH
	"NumpadEnter":    96, // KEY_KPENTER
}

// linuxUnmapped is empty: every name in the vocabulary has an evdev code. It
// exists so the shared table test reads the same on all platforms.
var linuxUnmapped = map[string]string{}

// platformTable exposes this platform's mapping to the shared table test.
func platformTable() (mapped map[string]keyCode, unmapped map[string]string) {
	return linuxKeys, linuxUnmapped
}
