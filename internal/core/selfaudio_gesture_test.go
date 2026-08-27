package core

import (
	"sync"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// One gesture is one transition.
//
// The mute and deafen buttons were unstable because a single gesture reached
// core as two independent calls, which Wails dispatches on a pool of worker
// goroutines (internal/assetserver: ServeWebViewRequest) and therefore does
// not order. The pair that came out of the race could be illegal, and the
// server discards an illegal pair instead of correcting it.

// selfAudioWrites returns the pairs the controller was asked to publish.
func selfAudioWrites(c *fakeController) []domain.SelfAudioState {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]domain.SelfAudioState, len(c.selfMutes))
	for i := range c.selfMutes {
		out[i] = domain.SelfAudioState{Muted: c.selfMutes[i], Deafened: c.selfDeafs[i]}
	}
	return out
}

// Deafened and unmuted is not a state the protocol has. Murmur v1.5.915
// answers {self_mute:false, self_deaf:true} by forcing the mute back on and
// keeping the deafen (verified against the live stand), so a client that asks
// for it loses the request and shows a state nobody else can see. Opening the
// microphone therefore lifts the deafen, which is also what murmur itself does
// with {self_mute:false} and what every user expects from Discord.
func TestUnmutingLiftsTheDeafen(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	app.SetDeafen(true)
	app.SetMute(false)

	if got := app.SelfAudio(); got.Muted || got.Deafened {
		t.Fatalf("state after unmuting = %+v, want both cleared", got)
	}
	got := voice.snapshot()
	if n := len(got.deafens); n == 0 || got.deafens[n-1] {
		t.Fatalf("engine deafens = %v, want the last one false", got.deafens)
	}
	for _, w := range selfAudioWrites(ctrl) {
		if w.Deafened && !w.Muted {
			t.Fatalf("published the illegal pair %+v; the server discards it", w)
		}
	}
}

// The same rule from the tray, which has no way to know about the other flag
// and calls the single-axis setter directly.
func TestSelfAudioNeverPublishesAnIllegalPair(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	app.SetVoice(&fakeVoice{})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := range 50 {
				app.SetMute(n%2 == 0)
				app.SetDeafen(n%3 == 0)
				if i%2 == 0 {
					app.SetMute(n%5 == 0)
				}
			}
		}(i)
	}
	wg.Wait()

	for _, w := range selfAudioWrites(ctrl) {
		if w.Deafened && !w.Muted {
			t.Fatalf("published the illegal pair %+v; the server discards it", w)
		}
	}
}

// Whatever order the racing gestures resolve in, the engine, the state and the
// wire have to agree on the winner. They used to disagree because each layer
// was updated outside the lock that decided the transition.
func TestConcurrentGesturesLeaveEveryLayerOnTheSameState(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := range 60 {
				app.SetMute(n%2 == 0)
				app.SetDeafen(i%2 == 0 && n%4 == 0)
			}
		}(i)
	}
	wg.Wait()
	// Settle on one known state so the comparison has an answer.
	app.SetDeafen(true)
	app.SetMute(false)

	state := app.SelfAudio()
	got := voice.snapshot()
	if n := len(got.mutes); n == 0 || got.mutes[n-1] != state.Muted {
		t.Fatalf("engine mute = %v, state = %+v", got.mutes, state)
	}
	if n := len(got.deafens); n == 0 || got.deafens[n-1] != state.Deafened {
		t.Fatalf("engine deafen = %v, state = %+v", got.deafens, state)
	}
	writes := selfAudioWrites(ctrl)
	if n := len(writes); n == 0 || writes[n-1] != state {
		t.Fatalf("last wire write = %+v, state = %+v", writes[len(writes)-1], state)
	}
}

// A tree that arrives while our own write is still on its way carries the
// state from before the gesture. Adopting it undoes the click the user just
// made - and because adopting does not write back, nothing ever corrects it.
func TestReconcileIgnoresEchoesWhileOurWriteIsPending(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	app.SetVoice(&fakeVoice{})

	app.SetDeafen(true)
	ctrl.setSelfAudioPending(true)

	app.HandleTree(treeWithSelf(false, false))

	if got := app.SelfAudio(); !got.Muted || !got.Deafened {
		t.Fatalf("state = %+v, want the gesture kept while our write is in flight", got)
	}

	// Draining the writer is not enough on its own: the packet has reached the
	// socket, not the room. Only the echo of our own pair hands authority back.
	ctrl.setSelfAudioPending(false)
	app.HandleTree(treeWithSelf(false, false))
	if got := app.SelfAudio(); !got.Muted || !got.Deafened {
		t.Fatalf("state = %+v, want the gesture kept until the room has answered", got)
	}

	app.HandleTree(treeWithSelf(true, true)) // the echo
	// Now the server speaks for itself again: an admin mute, or murmur's own
	// rules, still reach us.
	app.HandleTree(treeWithSelf(false, false))
	if got := app.SelfAudio(); got.Muted || got.Deafened {
		t.Fatalf("state = %+v, want the settled server state adopted", got)
	}
}

// The symptom the users reported, end to end: the click takes, and then quietly
// comes undone a moment later.
//
// Anything anybody else does - a join, a leave, somebody else's mute - makes
// the server send a whole tree, and a tree that crossed our packet on the wire
// still shows OUR previous flags. Nothing about it says so. Adopting it puts
// the microphone back where the user just moved it from, and since adoption
// deliberately does not write back, the room and the window disagree until the
// next click.
func TestAGestureSurvivesATreeThatCrossedItOnTheWire(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	app.SetVoice(&fakeVoice{})

	app.SetMute(true)
	// Somebody else joins a moment later. Their event brings a tree, and in it
	// we are still unmuted.
	app.HandleTree(treeWithSelf(false, false))
	if got := app.SelfAudio(); !got.Muted {
		t.Fatalf("state = %+v, want the mute to survive a tree older than it", got)
	}

	// The room catches up, and from then on it is the authority again.
	app.HandleTree(treeWithSelf(true, false))
	app.HandleTree(treeWithSelf(false, false))
	if got := app.SelfAudio(); got.Muted {
		t.Fatalf("state = %+v, want the server unmute adopted once it had settled", got)
	}
}

// treeWithSelf is a room holding nobody but us, with the flags the server
// reports for our own row.
func treeWithSelf(muted, deafened bool) domain.ChannelNode {
	return domain.ChannelNode{
		ID:   0,
		Name: "Root",
		Users: []domain.UserInfo{{
			Session: 1, Name: "me", IsSelf: true,
			SelfMute: muted, SelfDeaf: deafened,
		}},
	}
}

// Toggling has to decide from inside the transition, or two clicks arriving
// together decide from the same stale value and become one change.
//
// This is not hypothetical timing: the window, the tray and the services layer
// all reach the state from different goroutines, and Wails hands two calls from
// one window to different workers with no order between them. A caller that
// reads and then sets has a window between the two; a caller that flips under
// the lock has none.
func TestTogglingIsDecidedUnderTheLock(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	app.SetVoice(&fakeVoice{})

	// Counting the changes, not the final state: with lost updates the parity
	// is merely random, so a test on the end state passes half the time and
	// proves nothing either way. N flips must produce exactly N changes, and
	// the engine sees one call per change.
	voice := &fakeVoice{}
	app.SetVoice(voice)

	const flips = 400
	var wg sync.WaitGroup
	for range flips {
		wg.Add(1)
		go func() { defer wg.Done(); app.ToggleMute() }()
	}
	wg.Wait()

	got := voice.snapshot()
	if len(got.mutes) != flips {
		t.Fatalf("%d flips produced %d changes: %d of them read a value another had already replaced",
			flips, len(got.mutes), flips-len(got.mutes))
	}
	// And they alternate, because each one flipped what the previous left.
	for i, muted := range got.mutes {
		if want := i%2 == 0; muted != want {
			t.Fatalf("change %d set muted=%v, want %v: the flips did not alternate", i, muted, want)
		}
	}
}

// Toggling deafen carries the microphone with it, and lifting it gives both
// back - the same rule SetDeafen follows, because it is the same transition.
func TestTogglingDeafenCarriesTheMicrophone(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	app.SetVoice(&fakeVoice{})

	app.ToggleDeafen()
	if got := app.SelfAudio(); !got.Deafened || !got.Muted {
		t.Fatalf("state = %+v, want deafened to take the microphone with it", got)
	}
	app.ToggleDeafen()
	if got := app.SelfAudio(); got.Deafened || got.Muted {
		t.Fatalf("state = %+v, want both given back", got)
	}

	// Unmuting while deafened lifts the deafen, so the next mute toggle starts
	// from a state the server would accept.
	app.ToggleDeafen()
	app.ToggleMute()
	if got := app.SelfAudio(); got.Muted || got.Deafened {
		t.Fatalf("state = %+v, want unmuting to have lifted the deafen", got)
	}
}
