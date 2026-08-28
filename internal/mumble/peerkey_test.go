package mumble

import (
	"testing"

	"github.com/LywwKkA-aD/gumble/gumble"
)

// Two people with no certificate are two people.
//
// This is the failure the fallback exists for, and it was not reachable while
// every Gul client carried a certificate to Murmur: with the hash as the only
// key, every anonymous peer shared the entry "", so turning one stranger down
// turned all of them down. The tunnel contract made every session anonymous,
// which made it reachable for everybody at once.
func TestAnonymousPeersAreNotOnePerson(t *testing.T) {
	t.Parallel()
	first := peerKey(&gumble.User{Session: 7})
	second := peerKey(&gumble.User{Session: 8})

	if first == second {
		t.Fatalf("two anonymous peers share the key %q; a setting for one is a setting for both", first)
	}
	if first == "" || second == "" {
		t.Fatal("an anonymous peer has no key at all, so every one of them shares the empty entry")
	}
}

// The rungs, and what each of them promises.
func TestPeerKeyPrefersWhatSurvivesAReconnect(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		user   *gumble.User
		want   string
		mortal bool
	}{
		"a certificate outranks everything": {
			user: &gumble.User{Session: 7, UserID: 3, Hash: "abc"},
			want: peerKeyHash + "abc",
		},
		"a registration outranks the session": {
			user: &gumble.User{Session: 7, UserID: 3},
			want: peerKeyUser + "3",
		},
		"the session is the last resort": {
			user:   &gumble.User{Session: 7},
			want:   peerKeySession + "7",
			mortal: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := peerKey(tc.user)
			if got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
			// Murmur reuses session ids, so a setting kept under one of these
			// would eventually land on a stranger. Whoever stores against it
			// has to be told.
			if peerKeyIsMortal(got) != tc.mortal {
				t.Fatalf("key %q reports mortal=%v, want %v", got, !tc.mortal, tc.mortal)
			}
		})
	}
}

// A peer who arrives with nothing at all must still not collide with the next
// one, and a nil user must not produce a key that something could store under.
func TestPeerKeyOfNobodyIsNothing(t *testing.T) {
	t.Parallel()
	if got := peerKey(nil); got != "" {
		t.Fatalf("key for no user = %q, want empty", got)
	}
}
