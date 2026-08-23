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

// SetMute gates the microphone: the engine closes the transmission, the cue
// confirms it by ear, and the UI and the tray learn about it whichever path
// asked for it. Setting what is already set does nothing at all - a UI that
// re-sends its state must not produce a beep.
func (a *App) SetMute(muted bool) {
	a.mu.Lock()
	if a.selfMuted == muted {
		a.mu.Unlock()
		return
	}
	a.selfMuted = muted
	state, v := a.selfAudioLocked(), a.voice
	a.mu.Unlock()

	if v != nil {
		v.SetMute(muted)
	}
	a.publishSelfAudioToServer(state)
	cue := CueUnmuted
	if muted {
		cue = CueMuted
	}
	a.playCue(cue)
	a.publishSelfAudio(state)
}

// SetDeafen silences all remote streams locally and takes the microphone with
// it: deafen means "I am not in this conversation", and the server enforces
// the same rule (murmur forces self_mute alongside self_deaf), so a client
// that pretended otherwise would transmit room noise to people it cannot
// hear. Undeafening restores both. Every entry point - the window, the tray -
// goes through here, so they cannot disagree.
//
// No cue: the cues are mixed into the receive path deliberately
// (DECISIONS.md 2026-08-23) and the two clips this build has confirm the
// microphone, not the monitor. The implied mute is part of one gesture and
// does not beep on its own either.
func (a *App) SetDeafen(deafened bool) {
	a.mu.Lock()
	if a.selfDeafened == deafened {
		a.mu.Unlock()
		return
	}
	a.selfDeafened = deafened
	muteChanged := a.selfMuted != deafened
	a.selfMuted = deafened
	state, v := a.selfAudioLocked(), a.voice
	a.mu.Unlock()

	if v != nil {
		v.SetDeafen(deafened)
		if muteChanged {
			v.SetMute(deafened)
		}
	}
	a.publishSelfAudioToServer(state)
	a.publishSelfAudio(state)
}

// publishSelfAudioToServer tells the room what we hear and send. Offline the
// controller records the intent for the next connect.
func (a *App) publishSelfAudioToServer(state domain.SelfAudioState) {
	if ctrl, err := a.controller(); err == nil {
		ctrl.SetSelfAudio(state.Muted, state.Deafened)
	}
}

// reconcileSelfAudio adopts the state the server reports for our own row.
//
// The server is the authority: murmur applies rules of its own (deaf forces
// mute) and an admin can mute us outright. Our flags are only an intent until
// the server echoes them back, and a client that keeps arguing with the echo
// shows two contradicting indicators in one window - the member list draws
// the tree, the bottom bar draws the intent. Adopting costs nothing when they
// already agree, which is the normal case.
//
// No cue and no write back: this is where the state came from.
func (a *App) reconcileSelfAudio(root domain.ChannelNode) {
	self, ok := findSelf(root)
	if !ok {
		return
	}
	a.mu.Lock()
	if a.selfMuted == self.SelfMute && a.selfDeafened == self.SelfDeaf {
		a.mu.Unlock()
		return
	}
	a.selfMuted, a.selfDeafened = self.SelfMute, self.SelfDeaf
	state, v := a.selfAudioLocked(), a.voice
	a.mu.Unlock()

	if v != nil {
		v.SetMute(state.Muted)
		v.SetDeafen(state.Deafened)
	}
	a.log.Info("self audio state adopted from the server",
		"muted", state.Muted, "deafened", state.Deafened)
	a.publishSelfAudio(state)
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
func (a *App) publishSelfAudio(state domain.SelfAudioState) {
	a.emit(domain.EventAudioSelf, state)

	a.mu.Lock()
	observers := make([]func(TrayState), len(a.trayObservers))
	copy(observers, a.trayObservers)
	a.mu.Unlock()

	tray := trayStateOf(state)
	for _, fn := range observers {
		fn(tray)
	}
}
