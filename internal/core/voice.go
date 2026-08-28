package core

import (
	"errors"
	"fmt"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// GateMode is the transmit gate mode as the UI spells it and as the settings
// document stores it. The vocabulary and the bounds live in internal/config,
// so the file on disk and the running gate cannot drift apart. The audio
// engine has its own type; the adapter in main.go translates.
type GateMode = config.GateMode

const (
	// GateModeVAD transmits on the denoiser's speech probability.
	GateModeVAD = config.GateModeVAD
	// GateModePTT transmits while the push-to-talk key is held.
	GateModePTT = config.GateModePTT
)

var (
	ErrUnknownGateMode  = config.ErrUnknownGateMode
	ErrInvalidVADTuning = errors.New("invalid vad tuning")
	ErrInvalidCueVolume = errors.New("invalid cue volume")
)

// Cue identifies a UI sound. The engine synthesises the clips and mixes them
// into the receive path (DECISIONS.md 2026-08-23), so core only names the
// event; the adapter in main.go translates to the engine's own type.
type Cue int

const (
	// CueJoin and CueLeave mark somebody else arriving in or leaving the
	// channel we are in.
	CueJoin Cue = iota
	CueLeave
	// CueMuted and CueUnmuted confirm our own microphone toggle.
	CueMuted
	CueUnmuted
)

// VoiceEngine is what core needs from the audio engine; the adapter in
// main.go binds it to internal/audio and the device layer. Device IDs are
// opaque hex strings ("" = system default). The gate settings live on the
// engine and survive its restarts (device change, device lost); across
// application restarts they are restored from the settings document by
// UseSettings.
type VoiceEngine interface {
	Start(captureID, playbackID string) error
	Stop()
	SetMute(muted bool)
	SetDeafen(deafened bool)
	SetUserVolume(key string, volume float32)
	SetUserMute(key string, muted bool)
	SetGateMode(mode GateMode)
	SetVADTuning(open, close float32, hangoverMs int)
	SetPTT(held bool)
	SetCueVolume(volume float32)
	PlayCue(cue Cue)
	Devices() (playback, capture []domain.AudioDevice, err error)
}

// ParseGateMode validates the mode spelling that crosses the UI boundary.
// An unknown mode is an error rather than a fallback: answering a request for
// push-to-talk with voice activation leaves the microphone open.
func ParseGateMode(mode string) (GateMode, error) { return config.ParseGateMode(mode) }

// SetVoice injects the voice engine. Call before Connect.
func (a *App) SetVoice(v VoiceEngine) {
	a.mu.Lock()
	a.voice = v
	a.mu.Unlock()
}

func (a *App) voiceEngine() VoiceEngine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.voice
}

// startVoice launches the engine with the currently selected devices.
func (a *App) startVoice() {
	v := a.voiceEngine()
	if v == nil {
		return
	}
	a.mu.Lock()
	capID, pbID := a.captureID, a.playbackID
	a.mu.Unlock()
	go func() {
		if err := v.Start(capID, pbID); err != nil {
			a.log.Error("voice engine start", "error", err)
		}
	}()
}

func (a *App) stopVoice() {
	if v := a.voiceEngine(); v != nil {
		go v.Stop()
	}
}

// SetUserVolume sets the per-user gain by the peer key (mumble/peerkey.go), so
// it survives the peer reconnecting.
func (a *App) SetUserVolume(key string, volume float32) {
	if v := a.voiceEngine(); v != nil {
		v.SetUserVolume(key, volume)
	}
}

// SetUserMute silences one participant locally, or lets them back in, keyed
// by the same peer key as the gain.
//
// It is not the gain set to zero: the engine keeps the gain the user chose and
// gives it back on unmute (internal/audio/users.go). It is not the Mumble mute
// on the wire either - nothing is sent, and the other person is never told.
//
// NOT persisted, on purpose and by precedent: per-user gain is not persisted
// either. Both live in the running engine and survive a peer reconnecting
// within the session, and both start clean on the next run. Persisting the
// mute alone would leave a client that silently drops somebody with no visible
// reason after a restart, while the volume the user set beside it came back at
// unity. If one of them ever earns a home in the settings document, they take
// it together, keyed per server.
func (a *App) SetUserMute(key string, muted bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetUserMute(key, muted)
	}
}

// SetGateMode switches the transmit gate between voice activation and
// push-to-talk, and remembers the choice.
func (a *App) SetGateMode(mode string) error {
	m, err := ParseGateMode(mode)
	if err != nil {
		return err
	}
	if v := a.voiceEngine(); v != nil {
		v.SetGateMode(m)
	}
	a.updateSettings(func(c *config.Config) { c.Gate.Mode = m })
	// Only push-to-talk watches a key globally; leaving it means the watch
	// stops and the transmission it may hold open is closed (hotkey.go).
	a.applyGlobalPTT()
	return nil
}

// SetVADTuning sets the voice-activation threshold and the hangover tail, and
// remembers both. Only the opening edge is a decision: the closing edge of the
// hysteresis band is derived from it (config.CloseThreshold), so the gate the
// document describes is the gate the session runs. The engine clamps too, but
// a request it would have to bend is a UI defect, not a setting: report it
// instead of applying something the user did not ask for.
func (a *App) SetVADTuning(open float64, hangoverMs int) error {
	switch {
	case !config.ValidOpenThreshold(open):
		return fmt.Errorf("%w: open threshold %v outside [0, 1]", ErrInvalidVADTuning, open)
	case !config.ValidHangoverMs(hangoverMs):
		return fmt.Errorf("%w: hangover %d ms outside [0, %d]",
			ErrInvalidVADTuning, hangoverMs, config.MaxHangoverMs)
	}
	applyVADTuning(a.voiceEngine(), open, hangoverMs)
	a.updateSettings(func(c *config.Config) {
		c.Gate.OpenThreshold = open
		c.Gate.HangoverMs = hangoverMs
	})
	return nil
}

// applyGate pushes a stored gate section onto the engine. Called on startup,
// once the engine exists and before it is started.
func applyGate(v VoiceEngine, gate config.Gate) {
	if v == nil {
		return
	}
	v.SetGateMode(gate.Mode)
	applyVADTuning(v, gate.OpenThreshold, gate.HangoverMs)
}

// SetCueVolume sets the loudness of the UI cues and remembers it. The engine
// clamps as well, but a request it would have to bend is a UI defect, not a
// setting: report it instead of storing something the user did not ask for.
func (a *App) SetCueVolume(volume float64) error {
	if !config.ValidCueVolume(volume) {
		return fmt.Errorf("%w: cue volume %v outside [0, 1]", ErrInvalidCueVolume, volume)
	}
	if v := a.voiceEngine(); v != nil {
		v.SetCueVolume(float32(volume))
	}
	a.updateSettings(func(c *config.Config) { c.Audio.CueVolume = volume })
	return nil
}

// playCue asks the engine for a UI sound. Never blocks and never fails: a cue
// that arrives while the engine is stopped is simply not heard.
func (a *App) playCue(cue Cue) {
	if v := a.voiceEngine(); v != nil {
		v.PlayCue(cue)
	}
}

// applyCueVolume pushes a stored cue gain onto the engine.
func applyCueVolume(v VoiceEngine, volume float64) {
	if v == nil {
		return
	}
	v.SetCueVolume(float32(volume))
}

func applyVADTuning(v VoiceEngine, open float64, hangoverMs int) {
	if v == nil {
		return
	}
	v.SetVADTuning(float32(open), float32(config.CloseThreshold(open)), hangoverMs)
}

// SetPTT reports whether the push-to-talk key is held. It runs on every key
// transition from the window listener and from the global key. Besides the
// engine atomic it pushes EventAudioPTT so the microphone indicator tracks the
// global key too (it fires with the window unfocused, where the window
// listener never runs); the push is deduplicated so a repeated state is silent.
func (a *App) SetPTT(held bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetPTT(held)
	}
	// Two sources can drive the gate for the instant it takes the window
	// listener to stand down, and each Wails binding call runs on its own
	// goroutine. Publishing under the lock keeps the order the transitions
	// happened in: a release published before its own press would leave the
	// indicator lit with the microphone closed.
	a.pttMu.Lock()
	defer a.pttMu.Unlock()
	if a.pttHeld == held {
		return
	}
	a.pttHeld = held
	a.emit(domain.EventAudioPTT, domain.PTTState{Held: held})
}

// PTTState reports the current transmit state, so a UI that lost it (a
// reconnect clears its voice state) can re-seed instead of waiting for a
// transition that may never come while the key is held.
func (a *App) PTTState() domain.PTTState {
	a.pttMu.Lock()
	defer a.pttMu.Unlock()
	return domain.PTTState{Held: a.pttHeld}
}

// AudioDevices enumerates devices for the settings UI.
func (a *App) AudioDevices() (playback, capture []domain.AudioDevice, err error) {
	v := a.voiceEngine()
	if v == nil {
		return nil, nil, nil
	}
	return v.Devices()
}

// SelectDevices remembers the chosen devices, in the session and in the
// settings document, and restarts the engine when it is running ("" keeps the
// system default).
func (a *App) SelectDevices(captureID, playbackID string) {
	a.mu.Lock()
	a.captureID, a.playbackID = captureID, playbackID
	running := a.status.State == domain.StateConnected || a.status.State == domain.StateReconnecting
	a.mu.Unlock()

	a.updateSettings(func(c *config.Config) {
		c.Audio.CaptureID, c.Audio.PlaybackID = captureID, playbackID
	})

	if v := a.voiceEngine(); v != nil && running {
		go func() {
			v.Stop()
			if err := v.Start(captureID, playbackID); err != nil {
				a.log.Error("voice engine restart", "error", err)
			}
		}()
	}
}

// HandleDeviceLost restarts the engine on the currently selected devices
// after one of them stopped (unplug, backend error). Falls back to the
// system defaults when the restart fails.
func (a *App) HandleDeviceLost() {
	v := a.voiceEngine()
	if v == nil {
		return
	}
	a.mu.Lock()
	capID, pbID := a.captureID, a.playbackID
	a.mu.Unlock()
	a.log.Warn("audio device lost, restarting engine")
	v.Stop()
	if err := v.Start(capID, pbID); err != nil {
		a.log.Error("engine restart on selected devices", "error", err)
		if err := v.Start("", ""); err != nil {
			a.log.Error("engine restart on default devices", "error", err)
		}
	}
}

// HandleTalking forwards a talking transition from the DSP goroutine to
// the UI. Must stay fast.
func (a *App) HandleTalking(session uint32, hash string, talking bool) {
	a.emit(domain.EventUserTalking, domain.TalkingEvent{
		Session: session,
		Hash:    hash,
		Talking: talking,
	})
}

// HandleSelfTalking publishes our own transmit state. It runs on the DSP
// goroutine, so it stays a plain forward.
func (a *App) HandleSelfTalking(talking bool) {
	a.emit(domain.EventAudioSelfTalking, domain.SelfTalkingState{Talking: talking})
}

// HandleLevels forwards meter values (already throttled by the engine).
func (a *App) HandleLevels(micDb, outDb float64) {
	a.emit(domain.EventAudioLevels, domain.AudioLevels{MicDb: micDb, OutDb: outDb})
}
