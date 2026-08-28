package mumble

import (
	"strings"
	"testing"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/identity"
)

// The check that turns the identity from the relay's word into arithmetic.
//
// The relay speaks Murmur's TLS now, so the certificate the server sees is one
// the relay presented. The client knows before it connects exactly which
// certificate that should be and exactly what Murmur will call it, so a relay
// that showed the server anything else is caught here rather than trusted.
func TestTheClientCatchesAServerThatKnowsItAsSomebodyElse(t *testing.T) {
	t.Parallel()
	const host = "murmur.example.test"
	master := make([]byte, identity.SeedBytes)
	for i := range master {
		master[i] = byte(i + 1)
	}
	mine, err := identity.ForHost(master, host)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	t.Run("the name we expect is accepted", func(t *testing.T) {
		t.Parallel()
		client := &gumble.Client{Self: &gumble.User{Hash: mine.Fingerprint}}
		if err := checkIdentity(client, host, master, testLogger(t)); err != nil {
			t.Fatalf("our own identity was refused: %v", err)
		}
	})

	t.Run("somebody else's name is refused", func(t *testing.T) {
		t.Parallel()
		client := &gumble.Client{Self: &gumble.User{Hash: strings.Repeat("ab", 20)}}
		err := checkIdentity(client, host, master, testLogger(t))
		if err == nil {
			t.Fatal("the client accepted being logged in as somebody else")
		}
		if !strings.Contains(err.Error(), mine.Fingerprint) {
			t.Fatalf("error = %v, want it to say who this client is", err)
		}
	})

	// A server that does not ask for a certificate reports an empty hash for
	// everybody. That is its choice to make and not an accusation.
	t.Run("a server that does not ask is not accused", func(t *testing.T) {
		t.Parallel()
		client := &gumble.Client{Self: &gumble.User{}}
		if err := checkIdentity(client, host, master, testLogger(t)); err != nil {
			t.Fatalf("an anonymous server was treated as hostile: %v", err)
		}
	})

	// Without a secret there is nothing to check against.
	t.Run("an anonymous client checks nothing", func(t *testing.T) {
		t.Parallel()
		client := &gumble.Client{Self: &gumble.User{Hash: "whatever"}}
		if err := checkIdentity(client, host, nil, testLogger(t)); err != nil {
			t.Fatalf("a client with no identity refused a session: %v", err)
		}
	})
}
