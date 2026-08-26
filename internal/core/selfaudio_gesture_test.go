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

	app.HandleTree(domain.ChannelNode{
		ID:    0,
		Name:  "Root",
		Users: []domain.UserInfo{{Session: 1, Name: "me", IsSelf: true}},
	})

	if got := app.SelfAudio(); !got.Muted || !got.Deafened {
		t.Fatalf("state = %+v, want the gesture kept while our write is in flight", got)
	}

	// Once the write has landed the server is the authority again: an admin
	// mute, or murmur's own rules, still reach us.
	ctrl.setSelfAudioPending(false)
	app.HandleTree(domain.ChannelNode{
		ID:    0,
		Name:  "Root",
		Users: []domain.UserInfo{{Session: 1, Name: "me", IsSelf: true}},
	})
	if got := app.SelfAudio(); got.Muted || got.Deafened {
		t.Fatalf("state = %+v, want the settled server state adopted", got)
	}
}

func TestNormalizeSelfAudio(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want domain.SelfAudioState }{
		{domain.SelfAudioState{}, domain.SelfAudioState{}},
		{domain.SelfAudioState{Muted: true}, domain.SelfAudioState{Muted: true}},
		{
			domain.SelfAudioState{Deafened: true},
			domain.SelfAudioState{Muted: true, Deafened: true},
		},
		{
			domain.SelfAudioState{Muted: true, Deafened: true},
			domain.SelfAudioState{Muted: true, Deafened: true},
		},
	}
	for _, c := range cases {
		if got := normalizeSelfAudio(c.in); got != c.want {
			t.Errorf("normalizeSelfAudio(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// A tree carrying the pair the protocol does not have is still adopted as a
// state the engine can hold. Murmur cannot produce it, but the engine's own
// invariant must not depend on that.
func TestAnIllegalTreeIsAdoptedAsALegalState(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	app.HandleTree(domain.ChannelNode{
		ID:   0,
		Name: "Root",
		Users: []domain.UserInfo{
			{Session: 1, Name: "me", IsSelf: true, SelfMute: false, SelfDeaf: true},
		},
	})

	if got := app.SelfAudio(); !got.Muted || !got.Deafened {
		t.Fatalf("state = %+v, want the deafen to carry the mute", got)
	}
	got := voice.snapshot()
	if n := len(got.mutes); n == 0 || !got.mutes[n-1] {
		t.Fatalf("engine mutes = %v, want the microphone shut", got.mutes)
	}
}

// blockingCueVoice holds a gesture inside its cue so a second one can overtake
// it. That is the race the generation counter exists for.
type blockingCueVoice struct {
	*fakeVoice
	entered chan struct{}
	release chan struct{}
}

func (v *blockingCueVoice) PlayCue(cue Cue) {
	v.fakeVoice.PlayCue(cue)
	close(v.entered)
	<-v.release
}

// The gesture that lost the race must not repaint the icons afterwards. It
// used to: the overtaken call published its own state on the way out, so the
// window and the tray ended up showing a state that had already been replaced
// - a mute glyph on someone who was not muted, or none on someone who was.
func TestAnOvertakenGestureDoesNotRepaintTheIcons(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	voice := &blockingCueVoice{
		fakeVoice: &fakeVoice{},
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	app.SetVoice(voice)

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.SetMute(true) // holds inside its cue
	}()
	<-voice.entered

	app.SetDeafen(true) // overtakes it and publishes {muted, deafened}
	close(voice.release)
	<-done

	states := selfAudioEvents(em)
	if len(states) == 0 {
		t.Fatal("nothing was published at all")
	}
	last := states[len(states)-1]
	if want := (domain.SelfAudioState{Muted: true, Deafened: true}); last != want {
		t.Fatalf("last published state = %+v, want %+v", last, want)
	}
	if got := app.SelfAudio(); last != got {
		t.Fatalf("published %+v but the state is %+v", last, got)
	}
}
