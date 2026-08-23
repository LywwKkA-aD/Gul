//go:build live

package mumble

import (
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// TestSelfAudioReachesOtherClients is the M4 smoke for the mute and deafen
// icons: A flips its state, B must see it in the tree it receives from the
// server. Without the protocol write the flags stay false forever.
// Run with the stand up: task murmur:up && go test -tags live ./internal/mumble -run TestSelfAudioReachesOtherClients
func TestSelfAudioReachesOtherClients(t *testing.T) {
	a := newLiveManager(t, "gul-selfaudio-a")
	defer a.mgr.Close()
	b := newLiveManager(t, "gul-selfaudio-b")
	defer b.mgr.Close()

	a.mgr.Connect("127.0.0.1:64738", "gul-selfaudio-a", "")
	b.mgr.Connect("127.0.0.1:64738", "gul-selfaudio-b", "")
	waitState(t, a, domain.StateConnected)
	waitState(t, b, domain.StateConnected)
	waitRoot(t, b)

	a.mgr.SetSelfMuted(true)
	a.mgr.SetSelfDeafened(true)
	waitSelfAudio(t, b, "gul-selfaudio-a", true, true)

	a.mgr.SetSelfDeafened(false)
	a.mgr.SetSelfMuted(false)
	waitSelfAudio(t, b, "gul-selfaudio-a", false, false)
}

// waitSelfAudio blocks until the observer's tree shows the named user with the
// expected flags.
func waitSelfAudio(t *testing.T, c *liveClient, name string, muted, deafened bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last domain.UserInfo
	for time.Now().Before(deadline) {
		c.mu.Lock()
		tree := c.tr
		c.mu.Unlock()
		if tree != nil {
			if u, ok := findUser(*tree, name); ok {
				last = u
				if u.SelfMute == muted && u.SelfDeaf == deafened {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never reported mute=%v deaf=%v; last seen mute=%v deaf=%v",
		name, muted, deafened, last.SelfMute, last.SelfDeaf)
}

// findUser walks the channel tree looking for a user by name.
func findUser(node domain.ChannelNode, name string) (domain.UserInfo, bool) {
	for _, u := range node.Users {
		if u.Name == name {
			return u, true
		}
	}
	for _, child := range node.Children {
		if u, ok := findUser(child, name); ok {
			return u, true
		}
	}
	return domain.UserInfo{}, false
}
