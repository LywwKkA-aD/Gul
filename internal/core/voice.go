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
	SetUserVolume(hash string, volume float32)
	SetGateMode(mode GateMode)
	SetVADTuning(open, close float32, hangoverMs int)
	SetPTT(held bool)
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

// SetMute gates the microphone; the engine closes the transmission.
func (a *App) SetMute(muted bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetMute(muted)
	}
}

// SetDeafen silences all remote streams locally.
func (a *App) SetDeafen(deafened bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetDeafen(deafened)
	}
}

// SetUserVolume sets the per-user gain by the stable certificate hash, so
// it survives the peer reconnecting.
func (a *App) SetUserVolume(hash string, volume float32) {
	if v := a.voiceEngine(); v != nil {
		v.SetUserVolume(hash, volume)
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

func applyVADTuning(v VoiceEngine, open float64, hangoverMs int) {
	if v == nil {
		return
	}
	v.SetVADTuning(float32(open), float32(config.CloseThreshold(open)), hangoverMs)
}

// SetPTT reports whether the push-to-talk key is held. It runs on every key
// transition and stays a plain forward; the engine only flips an atomic.
func (a *App) SetPTT(held bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetPTT(held)
	}
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

// HandleLevels forwards meter values (already throttled by the engine).
func (a *App) HandleLevels(micDb, outDb float64) {
	a.emit(domain.EventAudioLevels, domain.AudioLevels{MicDb: micDb, OutDb: outDb})
}
