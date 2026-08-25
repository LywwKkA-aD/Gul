//go:build darwin && cgo && live

package secret

import (
	"errors"
	"strings"
	"testing"
)

// A real round trip through the login keychain. Behind the "live" tag (task
// test:live) for the same reason as the murmur tests: it touches a resource
// of the machine it runs on, which a CI runner may not have unlocked. Every
// item it creates is deleted again, including on failure.

const liveService = "com.gulvox.gul.test.secret"

func liveStore(t *testing.T) Store {
	t.Helper()
	store := New(liveService)
	if !store.Available() {
		t.Skip("no usable keychain on this machine")
	}
	return store
}

func TestLiveRoundTrip(t *testing.T) {
	store := liveStore(t)
	const account = "mumble.example.com:64738"
	t.Cleanup(func() {
		if err := store.Delete(account); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	if _, found, err := store.Get(account); err != nil || found {
		t.Fatalf("before Set: got found=%v err=%v, want a clean slate", found, err)
	}

	if err := store.Set(account, "first-password"); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, found, err := store.Get(account)
	if err != nil || !found || value != "first-password" {
		t.Fatalf("get = %q, %v, %v; want the password back", value, found, err)
	}

	// A second Set updates in place. Delete-then-add would leave the account
	// with no password at all if the process died between the two calls.
	if err := store.Set(account, "second-password"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	value, found, err = store.Get(account)
	if err != nil || !found || value != "second-password" {
		t.Fatalf("get after overwrite = %q, %v, %v", value, found, err)
	}

	if err := store.Delete(account); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, err := store.Get(account); err != nil || found {
		t.Fatalf("after Delete: found=%v err=%v, want gone", found, err)
	}
}

// Deleting what is not there is the state the caller asked for, so ForgetServer
// on a machine that never stored a password is not an error.
func TestLiveDeleteMissingIsNotAnError(t *testing.T) {
	store := liveStore(t)
	if err := store.Delete("never.stored.example:64738"); err != nil {
		t.Fatalf("delete of a missing item: %v", err)
	}
}

// Accounts are independent keys, and a value with non-ASCII bytes survives
// the trip through CFData unchanged.
func TestLiveAccountsAreIndependent(t *testing.T) {
	store := liveStore(t)
	const (
		first  = "one.example.com:64738"
		second = "wss://two.example.com/mumble"
	)
	t.Cleanup(func() {
		_ = store.Delete(first)
		_ = store.Delete(second)
	})

	unicode := "пароль-Ω-" + strings.Repeat("z", 32)
	if err := store.Set(first, "one"); err != nil {
		t.Fatalf("set first: %v", err)
	}
	if err := store.Set(second, unicode); err != nil {
		t.Fatalf("set second: %v", err)
	}

	if value, _, _ := store.Get(first); value != "one" {
		t.Errorf("first = %q", value)
	}
	if value, _, _ := store.Get(second); value != unicode {
		t.Errorf("second = %q, want %q", value, unicode)
	}

	if err := store.Delete(first); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	if value, found, _ := store.Get(second); !found || value != unicode {
		t.Errorf("deleting one account disturbed the other")
	}
}

// The key shaping is not advisory: a request the store cannot key on is
// rejected before it reaches the keychain.
func TestLiveRejectsUnusableAccount(t *testing.T) {
	store := liveStore(t)
	if err := store.Set("host\x00evil", "pw"); !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("set with a NUL in the account: %v", err)
	}
	if err := store.Set("host.example:64738", ""); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("set with an empty secret: %v", err)
	}
}
