package hotkey

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// requiredGroups is the vocabulary the settings screen must be able to bind.
// It is spelled out rather than derived so that a table edit which quietly
// drops a group fails here.
func requiredGroups() map[string][]string {
	group := func(names ...string) []string { return names }
	letters := make([]string, 0, 26)
	for c := 'A'; c <= 'Z'; c++ {
		letters = append(letters, "Key"+string(c))
	}
	digits := make([]string, 0, 10)
	numpad := make([]string, 0, 10)
	for d := '0'; d <= '9'; d++ {
		digits = append(digits, "Digit"+string(d))
		numpad = append(numpad, "Numpad"+string(d))
	}
	functions := make([]string, 0, 24)
	for i := 1; i <= 24; i++ {
		functions = append(functions, fmt.Sprintf("F%d", i))
	}
	return map[string][]string{
		"letters":  letters,
		"digits":   digits,
		"function": functions,
		"numpad":   numpad,
		"editing":  group("Space", "Tab", "CapsLock", "Backquote"),
		"navigation": group("Insert", "Delete", "Home", "End", "PageUp", "PageDown",
			"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"),
		"modifiers": group("ControlLeft", "ControlRight", "ShiftLeft", "ShiftRight",
			"AltLeft", "AltRight", "MetaLeft", "MetaRight"),
	}
}

func TestVocabularyCoversEveryRequiredGroup(t *testing.T) {
	for name, keys := range requiredGroups() {
		for _, key := range keys {
			if !validKey(key) {
				t.Errorf("%s: %q is missing from the vocabulary", name, key)
			}
		}
	}
}

func TestVocabularyHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range canonicalKeys {
		if seen[name] {
			t.Fatalf("%q appears twice in the vocabulary", name)
		}
		seen[name] = true
	}
	if len(seen) != len(canonicalSet) {
		t.Fatalf("vocabulary set holds %d names for %d entries", len(canonicalSet), len(seen))
	}
}

func TestVocabularyRejectsWhatItDoesNotName(t *testing.T) {
	// The frontend hands over a raw KeyboardEvent.code, so anything outside
	// the table has to be refused rather than silently accepted.
	for _, name := range []string{
		"", " ", "Space ", "space", "SPACE", "Key", "KeyAA", "Key1",
		"Digit10", "F0", "F25", "Numpad10", "Control", "Shift", "Alt", "Meta",
		"MediaPlayPause", "AudioVolumeUp", "IntlBackslash", "ContextMenu",
		"ScrollLock", "PrintScreen", "Pause", "NumLock", "NumpadComma",
	} {
		if validKey(name) {
			t.Errorf("%q is accepted but is not part of the vocabulary", name)
		}
	}
}

func TestPlatformTableCoversTheVocabulary(t *testing.T) {
	mapped, unmapped := platformTable()
	if mapped == nil {
		t.Skipf("no key table on %s", runtime.GOOS)
	}
	for _, name := range canonicalKeys {
		_, isMapped := mapped[name]
		reason, isUnmapped := unmapped[name]
		switch {
		case isMapped && isUnmapped:
			t.Errorf("%q is both mapped and declared unmappable", name)
		case !isMapped && !isUnmapped:
			t.Errorf("%q has no code on %s and no reason why not", name, runtime.GOOS)
		case isUnmapped && strings.TrimSpace(reason) == "":
			t.Errorf("%q is declared unmappable with an empty reason", name)
		}
	}
}

func TestPlatformTableHoldsNothingOutsideTheVocabulary(t *testing.T) {
	mapped, unmapped := platformTable()
	if mapped == nil {
		t.Skipf("no key table on %s", runtime.GOOS)
	}
	// A misspelled entry here would be dead weight that no Watch can reach.
	for _, name := range sortedKeys(mapped) {
		if !validKey(name) {
			t.Errorf("%q is in the %s table but not in the vocabulary", name, runtime.GOOS)
		}
	}
	for name := range unmapped {
		if !validKey(name) {
			t.Errorf("%q is declared unmappable but is not in the vocabulary", name)
		}
	}
}

func TestPlatformCodesAreUnique(t *testing.T) {
	mapped, _ := platformTable()
	if mapped == nil {
		t.Skipf("no key table on %s", runtime.GOOS)
	}
	// Two names sharing a code means one of them watches the wrong key.
	owner := map[keyCode]string{}
	for _, name := range sortedKeys(mapped) {
		code := mapped[name]
		if first, clash := owner[code]; clash {
			t.Errorf("%q and %q share code %#x on %s", first, name, code, runtime.GOOS)
			continue
		}
		owner[code] = name
	}
}

func TestSortedKeysIsDeterministic(t *testing.T) {
	table := map[string]keyCode{"KeyZ": 3, "KeyA": 1, "KeyM": 2}
	got := sortedKeys(table)
	want := []string{"KeyA", "KeyM", "KeyZ"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedKeys = %v, want %v", got, want)
		}
	}
}

func TestFunctionKeyNamesRunOneToTwentyFour(t *testing.T) {
	names := functionKeyNames()
	if len(names) != 24 {
		t.Fatalf("function keys = %d, want 24", len(names))
	}
	if names[0] != "F1" || names[8] != "F9" || names[9] != "F10" || names[23] != "F24" {
		t.Fatalf("function key names are out of order: %v", names)
	}
}
