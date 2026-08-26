package core

import "github.com/LywwKkA-aD/Gul/internal/domain"

// Self audio state: the microphone gate the user controls (mute) and the local
// silence of everybody else (deafen).
//
// Core owns both, because they are reachable from more than one place - the
// window, the system tray, and whatever comes next. Every path lands here, so
// the engine, the tray and the UI cannot disagree about what is on.

// Tray labels and tooltips are the only user-facing strings in core. They live
// here rather than in main.go so the state and the words for it stay together
// and can be tested.
const (
	trayTooltipIdle     = "Gul"
	trayTooltipMuted    = "Gul — микрофон выключен"
	trayTooltipDeafened = "Gul — звук выключен"
	trayTooltipBoth     = "Gul — микрофон и звук выключены"
)

// TrayIcon names which glyph the system tray shows.
type TrayIcon int

const (
	// TrayIconMic is the plain microphone.
	TrayIconMic TrayIcon = iota
	// TrayIconMicMuted is the crossed-out microphone.
	TrayIconMicMuted
)

// TrayState is everything the system tray renders. Deriving it here keeps the
// tray in main.go down to four setter calls and puts the rules under test.
//
// The icon follows the microphone alone: deafen silences other people and says
// nothing about what we are sending, so it belongs in the tooltip, not in the
// glyph.
type TrayState struct {
	Muted    bool
	Deafened bool
	Icon     TrayIcon
	Tooltip  string
}

// trayStateOf derives the tray rendering from the self audio state.
func trayStateOf(s domain.SelfAudioState) TrayState {
	state := TrayState{Muted: s.Muted, Deafened: s.Deafened, Icon: TrayIconMic}
	if s.Muted {
		state.Icon = TrayIconMicMuted
	}
	switch {
	case s.Muted && s.Deafened:
		state.Tooltip = trayTooltipBoth
	case s.Muted:
		state.Tooltip = trayTooltipMuted
	case s.Deafened:
		state.Tooltip = trayTooltipDeafened
	default:
		state.Tooltip = trayTooltipIdle
	}
	return state
}

// SelfAudio returns the current microphone and monitor state, for a UI that
// has just mounted and for the tray at startup.
func (a *App) SelfAudio() domain.SelfAudioState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.selfAudioLocked()
}

// TrayState returns what the system tray should render right now.
func (a *App) TrayState() TrayState { return trayStateOf(a.SelfAudio()) }

// OnTrayState registers an observer of the tray rendering. Observers run on
// the goroutine that changed the state, in registration order, with no lock
// held; one that touches native state has to marshal onto the main thread
// itself. Call before the application runs.
func (a *App) OnTrayState(fn func(TrayState)) {
	if fn == nil {
		return
	}
	a.mu.Lock()
	a.trayObservers = append(a.trayObservers, fn)
	a.mu.Unlock()
}

// selfAudioOptions says what a transition does besides changing the state.
type selfAudioOptions struct {
	// cue plays the microphone clip when the mute flag moves. Deafen has no
	// clip of its own (DECISIONS.md 2026-08-23) and the mute it carries is
	// part of the same gesture, so it stays silent.
	cue bool
	// toServer publishes the new state to the room. Off when the state came
	// from the room in the first place.
	toServer bool
}

// normalizeSelfAudio applies the one rule the protocol has: deafened implies
// muted.
//
// Murmur enforces it and does not negotiate. Against v1.5.915 on the live
// stand, {self_mute:false, self_deaf:true} comes back as {true, true}: the
// server forces the microphone shut and keeps the deafen, discarding our flag
// without an error. The mirror rule is that clearing self_mute clears
// self_deaf with it. A client that lets the illegal pair out loses the request
// silently and then draws a state nobody else can see.
func normalizeSelfAudio(s domain.SelfAudioState) domain.SelfAudioState {
	if s.Deafened {
		s.Muted = true
	}
	return s
}

// applySelfAudio is the one transition for the microphone and the monitor.
//
// The whole decision happens under a single lock - the next state, the engine,
// and the intent handed to the transport - so two gestures racing (the window
// against the tray, or two fast clicks that Wails hands to different worker
// goroutines) can only resolve to one of them, never to a mixture. Splitting
// this across two calls was what made the buttons unstable: each call decided
// on a state the other had already moved on from, and the pair that reached
// the wire was sometimes the illegal one the server throws away.
//
// next receives the current state and returns the wanted one; it runs under
// the lock and must not call back into the App.
//
// Only the cue and the notifications happen outside the lock, because an
// observer may call back into the App.
func (a *App) applySelfAudio(
	opts selfAudioOptions,
	next func(domain.SelfAudioState) domain.SelfAudioState,
) (domain.SelfAudioState, bool) {
	a.mu.Lock()
	cur := a.selfAudioLocked()
	want := normalizeSelfAudio(next(cur))
	if want == cur {
		a.mu.Unlock()
		return cur, false
	}
	a.selfMuted, a.selfDeafened = want.Muted, want.Deafened
	a.selfAudioGen++
	gen := a.selfAudioGen
	// Both are plain atomic stores in the engine, so the lock costs nothing
	// and buys the guarantee that the engine can never end up on the loser.
	if v := a.voice; v != nil {
		if want.Muted != cur.Muted {
			v.SetMute(want.Muted)
		}
		if want.Deafened != cur.Deafened {
			v.SetDeafen(want.Deafened)
		}
	}
	// The transport only records the intent here and writes it from its own
	// goroutine (internal/mumble/selfaudio.go), so this never blocks.
	if opts.toServer && a.ctrl != nil {
		a.ctrl.SetSelfAudio(want.Muted, want.Deafened)
	}
	a.mu.Unlock()

	if opts.cue && want.Muted != cur.Muted {
		cue := CueUnmuted
		if want.Muted {
			cue = CueMuted
		}
		a.playCue(cue)
	}
	a.publishSelfAudio(gen, want)
	return want, true
}

// SetMute gates the microphone: the engine closes the transmission, the cue
// confirms it by ear, and the UI and the tray learn about it whichever path
// asked for it. Setting what is already set does nothing at all - a UI that
// re-sends its state must not produce a beep.
//
// Opening the microphone lifts the deafen with it. Talking into ears that hear
// nothing makes no sense, it is the behaviour everyone knows from Discord, and
// it is what the server does anyway: murmur clears self_deaf when self_mute
// goes off.
func (a *App) SetMute(muted bool) {
	a.applySelfAudio(
		selfAudioOptions{cue: true, toServer: true},
		func(cur domain.SelfAudioState) domain.SelfAudioState {
			return domain.SelfAudioState{Muted: muted, Deafened: cur.Deafened && muted}
		},
	)
}

// SetDeafen silences all remote streams locally and takes the microphone with
// it: deafen means "I am not in this conversation", and the server enforces
// the same rule, so a client that pretended otherwise would transmit room
// noise to people it cannot hear. Undeafening restores both. Every entry point
// - the window, the tray - goes through here, so they cannot disagree.
//
// No cue: the cues are mixed into the receive path deliberately
// (DECISIONS.md 2026-08-23) and the two clips this build has confirm the
// microphone, not the monitor. The implied mute is part of one gesture and
// does not beep on its own either.
func (a *App) SetDeafen(deafened bool) {
	a.applySelfAudio(
		selfAudioOptions{toServer: true},
		func(domain.SelfAudioState) domain.SelfAudioState {
			return domain.SelfAudioState{Muted: deafened, Deafened: deafened}
		},
	)
}

// reconcileSelfAudio adopts the state the server reports for our own row.
//
// The server is the authority: our flags are only an intent until it echoes
// them back, and a client that keeps arguing with the echo shows two
// contradicting indicators in one window - the member list draws the tree, the
// bottom bar draws the intent.
//
// A tree that arrives while our own write is still on its way is not the
// server's opinion, though: these two flags carry the client's own state
// (snapshot.go reads gumble's SelfMuted and SelfDeafened), so an unsettled
// tree can only be showing the intent we have already replaced. Adopting it
// would undo the click the user just made - permanently, because adopting
// deliberately does not write back. So the echo is authority only once our own
// writes have drained.
//
// No cue: this is where the state came from.
func (a *App) reconcileSelfAudio(root domain.ChannelNode) {
	self, ok := findSelf(root)
	if !ok {
		return
	}
	if ctrl, err := a.controller(); err == nil && ctrl.SelfAudioPending() {
		return
	}
	server := domain.SelfAudioState{Muted: self.SelfMute, Deafened: self.SelfDeaf}
	state, changed := a.applySelfAudio(
		selfAudioOptions{},
		func(domain.SelfAudioState) domain.SelfAudioState { return server },
	)
	if changed {
		a.log.Info("self audio state adopted from the server",
			"muted", state.Muted, "deafened", state.Deafened)
	}
}

// findSelf locates our own row in a channel tree.
func findSelf(node domain.ChannelNode) (domain.UserInfo, bool) {
	for _, u := range node.Users {
		if u.IsSelf {
			return u, true
		}
	}
	for _, child := range node.Children {
		if u, ok := findSelf(child); ok {
			return u, true
		}
	}
	return domain.UserInfo{}, false
}

// selfAudioLocked reads the state. Caller holds a.mu.
func (a *App) selfAudioLocked() domain.SelfAudioState {
	return domain.SelfAudioState{Muted: a.selfMuted, Deafened: a.selfDeafened}
}

// publishSelfAudio pushes one change to the UI and to the tray. Called with no
// lock held: an observer may call back into the App.
//
// gen is the transition this state belongs to. A gesture that lost the race
// stops here rather than repainting the icons with the state the winner has
// already replaced - that stale repaint is what left a mute glyph on a user
// who was not muted, and left an unmuted user with no glyph at all.
func (a *App) publishSelfAudio(gen uint64, state domain.SelfAudioState) {
	a.mu.Lock()
	stale := gen != a.selfAudioGen
	observers := make([]func(TrayState), len(a.trayObservers))
	copy(observers, a.trayObservers)
	a.mu.Unlock()
	if stale {
		return
	}

	a.emit(domain.EventAudioSelf, state)
	tray := trayStateOf(state)
	for _, fn := range observers {
		fn(tray)
	}
}
