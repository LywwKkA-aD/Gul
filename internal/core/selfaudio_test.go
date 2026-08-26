package core

import (
	"sync"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// selfAudioEvents extracts the pushed self audio states in order.
func selfAudioEvents(em *fakeEmitter) []domain.SelfAudioState {
	var out []domain.SelfAudioState
	for _, ev := range em.all() {
		if ev.name != domain.EventAudioSelf {
			continue
		}
		if state, ok := ev.payload.(domain.SelfAudioState); ok {
			out = append(out, state)
		}
	}
	return out
}

// The engine, the UI and the tray all learn about a mute, in that order and
// exactly once. The last step also states the rule the protocol has: opening
// the microphone lifts the deafen, because deafened-and-unmuted is a pair the
// server discards rather than accepts (selfaudio.go).
func TestSetMuteReachesTheEngineTheUIAndTheTray(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	var mu sync.Mutex
	var seen []TrayState
	app.OnTrayState(func(state TrayState) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, state)
	})

	app.SetMute(true)
	app.SetDeafen(true)
	app.SetMute(false)

	got := voice.snapshot()
	if len(got.mutes) != 2 || !got.mutes[0] || got.mutes[1] {
		t.Errorf("engine mutes = %v, want [true false]", got.mutes)
	}
	// Opening the microphone lifts the deafen with it, so the engine hears
	// about both ends of the deafen and not just the start of it.
	if len(got.deafens) != 2 || !got.deafens[0] || got.deafens[1] {
		t.Errorf("engine deafens = %v, want [true false]", got.deafens)
	}

	events := selfAudioEvents(em)
	want := []domain.SelfAudioState{
		{Muted: true},
		{Muted: true, Deafened: true},
		{},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("event %d = %+v, want %+v", i, events[i], w)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("tray updates = %+v, want one per change", seen)
	}
	if !seen[0].Muted || seen[0].Icon != TrayIconMicMuted {
		t.Errorf("first tray update = %+v, want a muted microphone", seen[0])
	}
	if seen[2].Icon != TrayIconMic || seen[2].Deafened {
		t.Errorf("last tray update = %+v, want everything back open", seen[2])
	}
}

// A UI that re-sends what it already reported must not beep, push an event, or
// touch the engine: only a change is a change.
func TestSelfAudioIgnoresARedundantRequest(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	app.SetMute(false)
	app.SetDeafen(false)
	app.SetMute(true)
	app.SetMute(true)
	app.SetDeafen(true)
	app.SetDeafen(true)

	got := voice.snapshot()
	if len(got.mutes) != 1 || len(got.deafens) != 1 {
		t.Fatalf("engine calls = mutes %v, deafens %v; want one each", got.mutes, got.deafens)
	}
	if len(got.cues) != 1 || got.cues[0] != CueMuted {
		t.Fatalf("cues = %v, want a single CueMuted", got.cues)
	}
	if n := len(selfAudioEvents(em)); n != 2 {
		t.Fatalf("events = %d, want one per real change", n)
	}
}

// The cues confirm the microphone by ear, and they follow the state whichever
// path changed it.
func TestMuteCuesFollowTheState(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)

	app.SetMute(true)
	app.SetMute(false)
	app.SetDeafen(true)
	app.SetDeafen(false)

	got := voice.snapshot().cues
	if len(got) != 2 || got[0] != CueMuted || got[1] != CueUnmuted {
		t.Fatalf("cues = %v, want [CueMuted CueUnmuted] - deafen has no clip", got)
	}
}

func TestSelfAudioSnapshot(t *testing.T) {
	t.Parallel()
	app, _ := newVoiceApp(t)

	if got := app.SelfAudio(); got.Muted || got.Deafened {
		t.Fatalf("SelfAudio() = %+v, want a fresh session", got)
	}
	app.SetMute(true)
	if got := app.SelfAudio(); !got.Muted || got.Deafened {
		t.Fatalf("SelfAudio() = %+v, want muted only", got)
	}
	if got := app.TrayState(); !got.Muted || got.Icon != TrayIconMicMuted {
		t.Fatalf("TrayState() = %+v, want the muted rendering", got)
	}
}

// The tooltip is the only place deafen is visible: the glyph tracks the
// microphone, because that is what other people hear.
func TestTrayStateDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state       domain.SelfAudioState
		wantIcon    TrayIcon
		wantTooltip string
	}{
		{domain.SelfAudioState{}, TrayIconMic, trayTooltipIdle},
		{domain.SelfAudioState{Muted: true}, TrayIconMicMuted, trayTooltipMuted},
		{domain.SelfAudioState{Deafened: true}, TrayIconMic, trayTooltipDeafened},
		{domain.SelfAudioState{Muted: true, Deafened: true}, TrayIconMicMuted, trayTooltipBoth},
	}
	for _, c := range cases {
		got := trayStateOf(c.state)
		if got.Muted != c.state.Muted || got.Deafened != c.state.Deafened {
			t.Errorf("trayStateOf(%+v) lost the state: %+v", c.state, got)
		}
		if got.Icon != c.wantIcon {
			t.Errorf("trayStateOf(%+v).Icon = %v, want %v", c.state, got.Icon, c.wantIcon)
		}
		if got.Tooltip != c.wantTooltip {
			t.Errorf("trayStateOf(%+v).Tooltip = %q, want %q", c.state, got.Tooltip, c.wantTooltip)
		}
	}
}

// Mute is reachable from the window and from the tray at the same time; the
// state that comes out of a race is still one of the two, and every observer
// sees a consistent one.
func TestSelfAudioIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)
	app.OnTrayState(func(TrayState) {})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				app.SetMute(n%2 == 0)
				app.SetDeafen(i%2 == 0)
				app.SelfAudio()
				app.TrayState()
			}
		}(i)
	}
	wg.Wait()

	state := app.SelfAudio()
	if got := voice.snapshot().mutes; len(got) == 0 {
		t.Fatal("the engine was never told anything")
	}
	if app.TrayState().Muted != state.Muted {
		t.Fatal("the tray rendering disagrees with the state it was derived from")
	}
}

// SetMute and SetDeafen publish the audio state to the server so other
// participants see the icons; a redundant request sends nothing.
func TestSelfAudioReachesTheServer(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	app.SetVoice(&fakeVoice{})

	app.SetMute(true)
	app.SetMute(true) // no change, no wire write
	app.SetDeafen(true)
	app.SetDeafen(false)

	ctrl.mu.Lock()
	mutes := append([]bool(nil), ctrl.selfMutes...)
	deafs := append([]bool(nil), ctrl.selfDeafs...)
	ctrl.mu.Unlock()

	// Both flags travel together on every change, and deafen carries the
	// microphone with it - undeafening must state mute=false explicitly, or
	// the server keeps the mute it forced and the user stays inaudible.
	if want := []bool{true, true, false}; !equalBools(mutes, want) {
		t.Fatalf("self mutes = %v, want %v", mutes, want)
	}
	if want := []bool{false, true, false}; !equalBools(deafs, want) {
		t.Fatalf("self deafs = %v, want %v", deafs, want)
	}
	if got := app.SelfAudio(); got.Muted || got.Deafened {
		t.Fatalf("state after undeafen = %+v, want both cleared", got)
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The server is the authority on our own audio state: it applies rules of its
// own and an admin can mute us. A tree that disagrees with the local intent
// is adopted, published, and applied to the engine - without writing back.
func TestSelfAudioAdoptsTheServerState(t *testing.T) {
	t.Parallel()
	app, ctrl, em := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	app.HandleTree(domain.ChannelNode{
		ID:    0,
		Name:  "Root",
		Users: []domain.UserInfo{{Session: 1, Name: "me", IsSelf: true, SelfMute: true}},
	})

	if got := app.SelfAudio(); !got.Muted || got.Deafened {
		t.Fatalf("state = %+v, want muted adopted from the tree", got)
	}
	states := selfAudioEvents(em)
	if len(states) != 1 || !states[0].Muted {
		t.Fatalf("published states = %+v, want one muted", states)
	}
	voice.mu.Lock()
	mutes := append([]bool(nil), voice.mutes...)
	voice.mu.Unlock()
	if len(mutes) != 1 || !mutes[0] {
		t.Fatalf("engine mutes = %v, want [true]", mutes)
	}
	// Adopting must not echo back to the server: that is where it came from.
	ctrl.mu.Lock()
	writes := len(ctrl.selfMutes)
	ctrl.mu.Unlock()
	if writes != 0 {
		t.Fatalf("wrote self audio to the server %d times, want 0", writes)
	}

	// An agreeing tree changes nothing at all.
	app.HandleTree(domain.ChannelNode{
		ID:    0,
		Name:  "Root",
		Users: []domain.UserInfo{{Session: 1, Name: "me", IsSelf: true, SelfMute: true}},
	})
	if got := len(selfAudioEvents(em)); got != 1 {
		t.Fatalf("published %d states, want the agreeing tree to be silent", got)
	}
}
