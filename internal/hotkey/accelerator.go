package hotkey

// Translation from the KeyboardEvent.code vocabulary to the accelerator syntax
// Wails' GlobalShortcutManager parses (pkg/application/keys.go): a chain of
// modifiers and one final key, where the key is either a name from Wails'
// namedKeys table or a single printable character, matched case-insensitively.
//
// Only the Wayland toggle needs this. Every other backend addresses the key by
// its platform code and never goes near an accelerator string.
//
// Three groups have no accelerator and must be refused rather than approximated:
//
//   - the modifiers themselves (ControlLeft and friends) - the syntax can
//     express "Ctrl+A" but not Ctrl on its own, and Wails' parser reads a
//     trailing modifier as a missing key;
//   - the numeric keypad - the syntax has no way to say "keypad 5", so
//     "Numpad5" could only be sent as "5", which would bind the digit row and
//     hand the user a key they did not choose;
//   - CapsLock and Insert, which Wails' table simply does not carry.

// accelerators maps vocabulary names to accelerator keys. A name absent here
// cannot be a Wayland toggle.
var accelerators = buildAccelerators()

func buildAccelerators() map[string]string {
	table := make(map[string]string, 96)
	for c := 'A'; c <= 'Z'; c++ {
		table["Key"+string(c)] = string(c)
	}
	for d := '0'; d <= '9'; d++ {
		table["Digit"+string(d)] = string(d)
	}
	for _, name := range functionKeyNames() {
		table[name] = name
	}
	for name, accel := range map[string]string{
		"Space":     "Space",
		"Tab":       "Tab",
		"Escape":    "Escape",
		"Enter":     "Enter",
		"Backspace": "Backspace",
		"Delete":    "Delete",
		"Home":      "Home",
		"End":       "End",
		"PageUp":    "Page Up",
		"PageDown":  "Page Down",

		"ArrowUp":    "Up",
		"ArrowDown":  "Down",
		"ArrowLeft":  "Left",
		"ArrowRight": "Right",

		"Backquote":    "`",
		"Minus":        "-",
		"Equal":        "=",
		"BracketLeft":  "[",
		"BracketRight": "]",
		"Backslash":    "\\",
		"Semicolon":    ";",
		"Quote":        "'",
		"Comma":        ",",
		"Period":       ".",
		"Slash":        "/",
	} {
		table[name] = accel
	}
	return table
}

// acceleratorFor returns the accelerator string for a vocabulary name.
func acceleratorFor(name string) (string, bool) {
	accel, ok := accelerators[name]
	return accel, ok
}
