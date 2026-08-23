//go:build live

package mumble

import (
	"math"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
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

	a.mgr.SetSelfAudio(true, false)
	waitSelfAudio(t, b, "gul-selfaudio-a", true, false)

	// The deafen cycle is the one that used to strand the user: murmur forces
	// self_mute alongside self_deaf and never clears it, so a client that
	// sends the flags one at a time comes back permanently inaudible.
	a.mgr.SetSelfAudio(true, true)
	waitSelfAudio(t, b, "gul-selfaudio-a", true, true)

	a.mgr.SetSelfAudio(false, false)
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

// TestVoiceSurvivesADeafenCycle is the regression that matters to the user:
// the flags are only a symptom, what broke was that murmur stopped routing
// their voice. A deafen/undeafen round trip must leave them audible again.
func TestVoiceSurvivesADeafenCycle(t *testing.T) {
	a := newLiveManager(t, "gul-deafcycle-a")
	defer a.mgr.Close()
	b := newLiveManager(t, "gul-deafcycle-b")
	defer b.mgr.Close()

	a.mgr.Connect("127.0.0.1:64738", "gul-deafcycle-a", "")
	b.mgr.Connect("127.0.0.1:64738", "gul-deafcycle-b", "")
	waitState(t, a, domain.StateConnected)
	waitState(t, b, domain.StateConnected)
	waitRoot(t, b)

	a.mgr.SetSelfAudio(false, true)
	waitSelfAudio(t, b, "gul-deafcycle-a", true, true)
	a.mgr.SetSelfAudio(false, false)
	waitSelfAudio(t, b, "gul-deafcycle-a", false, false)

	if got := voiceFramesThrough(t, a, b); got == 0 {
		t.Fatal("no voice reached the room after a deafen cycle - the server still holds a forced mute")
	}
}

// voiceFramesThrough sends a second of tone from one manager and counts what
// the other receives.
func voiceFramesThrough(t *testing.T, from, to *liveClient) int {
	t.Helper()
	enc, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	frame := make([]int16, opus.FrameSize)
	const amp = 0.3 * 32767
	for i := range 100 {
		for n := range frame {
			tm := float64(i*opus.FrameSize+n) / opus.SampleRate
			frame[n] = int16(amp * math.Sin(2*math.Pi*440*tm))
		}
		data, err := enc.Encode(frame, nil)
		if err != nil {
			t.Fatalf("frame %d: encode: %v", i, err)
		}
		if err := from.mgr.SendVoice(data, false); err != nil {
			t.Fatalf("frame %d: send: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	clear(frame)
	if data, err := enc.Encode(frame, nil); err == nil {
		_ = from.mgr.SendVoice(data, true)
	}

	received := 0
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case p := <-to.mgr.VoicePackets():
			if p.Final {
				return received
			}
			received++
		case <-deadline.C:
			return received
		}
	}
}
