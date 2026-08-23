package hotkey

import (
	"strings"
	"testing"
)

// wailsNamedKeys is the namedKeys table from Wails v3.0.0-beta.11
// (pkg/application/keys.go), transcribed so that the accelerators this package
// derives can be checked against the grammar that will parse them without
// standing up an application.
var wailsNamedKeys = func() map[string]struct{} {
	set := map[string]struct{}{}
	for _, name := range []string{
		"backspace", "tab", "return", "enter", "escape", "left", "right",
		"up", "down", "space", "delete", "home", "end", "page up", "page down",
		"numlock",
	} {
		set[name] = struct{}{}
	}
	for i := 1; i <= 35; i++ {
		set["f"+itoa(i)] = struct{}{}
	}
	return set
}()

// parsableByWails mirrors parseAccelerator/parseKey from Wails: components are
// split on "+", the final one is the key, and a key is either a named key or a
// single printable character, matched case-insensitively.
func parsableByWails(accelerator string) bool {
	components := strings.Split(accelerator, "+")
	key := strings.ToLower(components[len(components)-1])
	if key == "plus" {
		return true
	}
	if _, named := wailsNamedKeys[key]; named {
		return true
	}
	return len([]byte(key)) == 1 && key[0] >= 0x20 && key[0] < 0x7f
}

func TestEveryAcceleratorParsesUnderTheWailsGrammar(t *testing.T) {
	for _, name := range canonicalKeys {
		accelerator, ok := acceleratorFor(name)
		if !ok {
			continue
		}
		if !parsableByWails(accelerator) {
			t.Errorf("%q derives accelerator %q, which Wails cannot parse", name, accelerator)
		}
	}
}

func TestAcceleratorsCarryNoSeparator(t *testing.T) {
	// "+" splits an accelerator into modifiers and a key, so a key that
	// contained one would be read as two components.
	for _, name := range canonicalKeys {
		if accelerator, ok := acceleratorFor(name); ok && strings.Contains(accelerator, "+") {
			t.Errorf("%q derives %q, which contains the component separator", name, accelerator)
		}
	}
}

func TestAcceleratorsAreDistinct(t *testing.T) {
	// Two keys deriving one accelerator would mean binding either of them
	// hands the user the other.
	owner := map[string]string{}
	for _, name := range canonicalKeys {
		accelerator, ok := acceleratorFor(name)
		if !ok {
			continue
		}
		if first, clash := owner[strings.ToLower(accelerator)]; clash {
			t.Errorf("%q and %q both derive %q", first, name, accelerator)
			continue
		}
		owner[strings.ToLower(accelerator)] = name
	}
}

func TestAcceleratorsForNamedKeys(t *testing.T) {
	for name, want := range map[string]string{
		"KeyA":         "A",
		"KeyZ":         "Z",
		"Digit0":       "0",
		"F1":           "F1",
		"F24":          "F24",
		"Space":        "Space",
		"Tab":          "Tab",
		"Escape":       "Escape",
		"Enter":        "Enter",
		"Backspace":    "Backspace",
		"Delete":       "Delete",
		"Home":         "Home",
		"End":          "End",
		"PageUp":       "Page Up",
		"PageDown":     "Page Down",
		"ArrowUp":      "Up",
		"ArrowDown":    "Down",
		"ArrowLeft":    "Left",
		"ArrowRight":   "Right",
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
		got, ok := acceleratorFor(name)
		if !ok {
			t.Errorf("acceleratorFor(%q) found nothing, want %q", name, want)
			continue
		}
		if got != want {
			t.Errorf("acceleratorFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestKeysWithNoAcceleratorForm(t *testing.T) {
	// Refusing these is the point: the accelerator grammar cannot express a
	// bare modifier or a keypad position, and "Numpad5" sent as "5" would
	// bind the digit row instead.
	for _, name := range []string{
		"ControlLeft", "ControlRight", "ShiftLeft", "ShiftRight",
		"AltLeft", "AltRight", "MetaLeft", "MetaRight",
		"CapsLock", "Insert",
		"Numpad0", "Numpad5", "Numpad9",
		"NumpadMultiply", "NumpadAdd", "NumpadSubtract", "NumpadDecimal",
		"NumpadDivide", "NumpadEnter",
	} {
		if accelerator, ok := acceleratorFor(name); ok {
			t.Errorf("acceleratorFor(%q) = %q, want no accelerator", name, accelerator)
		}
	}
}

func TestEveryOtherVocabularyKeyHasAnAccelerator(t *testing.T) {
	// The refusals above are the complete list; anything else missing an
	// accelerator would be a Wayland user unable to bind a perfectly ordinary
	// key.
	refused := map[string]bool{}
	for _, name := range []string{
		"ControlLeft", "ControlRight", "ShiftLeft", "ShiftRight",
		"AltLeft", "AltRight", "MetaLeft", "MetaRight",
		"CapsLock", "Insert",
		"Numpad0", "Numpad1", "Numpad2", "Numpad3", "Numpad4",
		"Numpad5", "Numpad6", "Numpad7", "Numpad8", "Numpad9",
		"NumpadMultiply", "NumpadAdd", "NumpadSubtract", "NumpadDecimal",
		"NumpadDivide", "NumpadEnter",
	} {
		refused[name] = true
	}
	for _, name := range canonicalKeys {
		_, ok := acceleratorFor(name)
		if ok == refused[name] {
			t.Errorf("%q: accelerator present = %v, refused = %v", name, ok, refused[name])
		}
	}
}

func TestAcceleratorsHoldNothingOutsideTheVocabulary(t *testing.T) {
	for name := range accelerators {
		if !validKey(name) {
			t.Errorf("%q has an accelerator but is not in the vocabulary", name)
		}
	}
}
