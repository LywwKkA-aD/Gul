package secret

import (
	"errors"
	"strings"
	"testing"
)

// The portable half of the package: the key shaping every backend shares and
// the error vocabulary a caller matches on. The backends themselves talk to a
// real credential store and are covered by the live test next to this one.

func TestAccountKeyTrimsAndKeeps(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"mumble.example.com:64738", "mumble.example.com:64738"},
		{"  mumble.example.com:64738  ", "mumble.example.com:64738"},
		{"\twss://murmur.gulvox.com/mumble\n", "wss://murmur.gulvox.com/mumble"},
		// Case is preserved: the key must match the address in the settings
		// document byte for byte, or one spelling would read another's
		// password.
		{"Mumble.Example.COM:64738", "Mumble.Example.COM:64738"},
		{strings.Repeat("a", maxAccountLen), strings.Repeat("a", maxAccountLen)},
	} {
		got, err := accountKey(tc.in)
		if err != nil {
			t.Fatalf("accountKey(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("accountKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAccountKeyRejects(t *testing.T) {
	for name, in := range map[string]string{
		"empty":            "",
		"whitespace only":  " \t\n ",
		"embedded NUL":     "host\x00evil",
		"trailing NUL":     "host\x00",
		"over the max len": strings.Repeat("a", maxAccountLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := accountKey(in); !errors.Is(err, ErrInvalidAccount) {
				t.Fatalf("accountKey(%q) error = %v, want ErrInvalidAccount", in, err)
			}
		})
	}
}

func TestCheckSecret(t *testing.T) {
	if err := checkSecret(strings.Repeat("x", maxSecretLen)); err != nil {
		t.Fatalf("secret at the limit: %v", err)
	}
	// An empty secret is Delete's business, not Set's: two spellings of one
	// state would give the backends two behaviours.
	if err := checkSecret(""); !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("empty secret error = %v, want ErrInvalidSecret", err)
	}
	if err := checkSecret(strings.Repeat("x", maxSecretLen+1)); !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("oversized secret error = %v, want ErrInvalidSecret", err)
	}
}

func TestUnavailableStoreSaysSo(t *testing.T) {
	store := unavailable{reason: "no store here"}

	if store.Available() {
		t.Fatal("Available reported true")
	}
	if err := store.Set("host", "pw"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Set error = %v, want ErrUnavailable", err)
	}
	if err := store.Delete("host"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Delete error = %v, want ErrUnavailable", err)
	}
	value, found, err := store.Get("host")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get error = %v, want ErrUnavailable", err)
	}
	if found || value != "" {
		t.Errorf("Get = %q, %v; want empty and not found", value, found)
	}
}

// A store with no service name would put Gul's items in an unnamed bucket
// shared with every other application that made the same mistake.
func TestNewWithoutServiceIsUnavailable(t *testing.T) {
	for _, service := range []string{"", "   "} {
		if New(service).Available() {
			t.Errorf("New(%q) reported an available store", service)
		}
	}
}

// New always returns something usable as a Store, whatever the platform.
func TestNewReturnsAStore(t *testing.T) {
	if New("com.gulvox.gul.test") == nil {
		t.Fatal("New returned nil")
	}
}
