//go:build live

package mumble

import (
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/hysteria"
	"github.com/LywwKkA-aD/Gul/internal/identity"
)

const relayLiveSecret = "relay live test password"

// The developer stand sets this SuperUser password. Both credentials are
// accepted by the local Hysteria fixture; production uses its own configuration.
const liveSuperUserPassword = "devsuperuser"

// localRelay keeps the shared live-test helper's historical name. It now runs
// the official Hysteria server and forwards directly to the local Murmur stand.
// There is one transport and one outer certificate; the third return value is
// retained for existing callers until their helper signatures are simplified.
func localRelay(t *testing.T) (endpoint, *tls.Config, *tls.Config) {
	t.Helper()
	proxy := startHysteriaFixture(t, "127.0.0.1:64738", "", []string{
		relayLiveSecret, liveSuperUserPassword,
	})
	return proxy.endpoint, proxy.roots, proxy.roots
}

func TestClientReachesMurmurThroughHysteria(t *testing.T) {
	ep, roots, _ := localRelay(t)
	session, err := Dial(DialConfig{
		Context: t.Context(), Address: ep.address, Username: "gul-hysteria-live",
		Password: relayLiveSecret, OuterRoots: roots,
	}, NewTOFUStore(t.TempDir(), testLogger(t)), testLogger(t))
	if err != nil {
		t.Fatalf("Mumble through Hysteria failed (is task murmur:up running?): %v", err)
	}
	defer session.Disconnect()
	if session.State() != "synced" {
		t.Fatalf("session = %s, want synced", session.State())
	}
}

// Murmur sees the same deterministic certificate previously derived by Gul.
// The certificate is now presented by the client inside end-to-end Mumble TLS.
func TestMurmurKnowsUsByTheNameWeDerived(t *testing.T) {
	ep, roots, _ := localRelay(t)
	master := make([]byte, identity.SeedBytes)
	for i := range master {
		master[i] = byte(i*7 + 1)
	}
	expected, err := identity.ForHost(master, ep.host)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), testLogger(t), Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.identitySeed = master
	manager.outerRoots = roots
	manager.Connect(ep.address, "gul-identity-live", relayLiveSecret)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		got := ""
		if client := manager.currentClient(); client != nil {
			client.Do(func() {
				if client.Self != nil {
					got = client.Self.Hash
				}
			})
		}
		if got != "" {
			if got != expected.Fingerprint {
				t.Fatal("Murmur reported a different identity")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Murmur never reported the client's certificate identity")
}

func TestHysteriaRefusesTheWrongPasswordBeforeMurmur(t *testing.T) {
	ep, roots, _ := localRelay(t)
	session, err := Dial(DialConfig{
		Context: t.Context(), Address: ep.address, Username: "gul-auth-live",
		Password: "incorrect-live-test-password", OuterRoots: roots,
	}, NewTOFUStore(t.TempDir(), testLogger(t)), testLogger(t))
	if session != nil {
		_ = session.Disconnect()
	}
	if !errors.Is(err, hysteria.ErrAuthentication) {
		t.Fatalf("wrong password returned %v, want Hysteria authentication error", err)
	}
}
