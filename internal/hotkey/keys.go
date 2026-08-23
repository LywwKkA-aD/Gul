package hotkey

import "sort"

// The key vocabulary: KeyboardEvent.code names, which describe a physical key
// position rather than the character it produces. The frontend hands these out
// verbatim from a keydown event, so anything a browser can report and this
// package refuses must fail loudly at Watch instead of turning into a watch
// that never fires.
//
// The list is deliberately closed. Media keys, Escape-adjacent laptop keys and
// the international extras differ enough between the three platforms that a
// name accepted here would map to nothing on at least one of them, and a key
// that works on one machine and not the next is worse than a rejected binding.

// canonicalKeys is every name this package accepts, in a stable order so that
// tests and diagnostics list them the same way twice.
var canonicalKeys = buildCanonicalKeys()

// canonicalSet is canonicalKeys as a set, for the Watch-time check.
var canonicalSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(canonicalKeys))
	for _, name := range canonicalKeys {
		set[name] = struct{}{}
	}
	return set
}()

func buildCanonicalKeys() []string {
	names := make([]string, 0, 128)
	for c := 'A'; c <= 'Z'; c++ {
		names = append(names, "Key"+string(c))
	}
	for d := '0'; d <= '9'; d++ {
		names = append(names, "Digit"+string(d))
	}
	names = append(names, functionKeyNames()...)
	names = append(names,
		"Space", "Tab", "Escape", "Enter", "Backspace", "CapsLock",
		"Backquote", "Minus", "Equal", "BracketLeft", "BracketRight",
		"Backslash", "Semicolon", "Quote", "Comma", "Period", "Slash",
		"Insert", "Delete", "Home", "End", "PageUp", "PageDown",
		"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
		"ControlLeft", "ControlRight", "ShiftLeft", "ShiftRight",
		"AltLeft", "AltRight", "MetaLeft", "MetaRight",
	)
	for d := '0'; d <= '9'; d++ {
		names = append(names, "Numpad"+string(d))
	}
	names = append(names,
		"NumpadMultiply", "NumpadAdd", "NumpadSubtract",
		"NumpadDecimal", "NumpadDivide", "NumpadEnter",
	)
	return names
}

// functionKeyNames returns F1..F24 in numeric order.
func functionKeyNames() []string {
	names := make([]string, 0, 24)
	for i := 1; i <= 24; i++ {
		names = append(names, "F"+itoa(i))
	}
	return names
}

// itoa avoids pulling strconv in for two digits.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// validKey reports whether name is part of the vocabulary.
func validKey(name string) bool {
	_, ok := canonicalSet[name]
	return ok
}

// keyCode is one platform's identifier for a physical key: a macOS virtual
// key code, a Windows virtual-key code or an X11 keycode. The poller only
// carries it between lookup and pressed and never interprets it.
type keyCode uint32

// sortedKeys returns the names of a platform table in sorted order, for
// deterministic test failures.
func sortedKeys(table map[string]keyCode) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
