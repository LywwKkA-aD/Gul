package core

import (
	"errors"
	"fmt"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// GateMode is the transmit gate mode as the UI spells it. The audio engine
// has its own type; the adapter in main.go translates, so nothing under core
// depends on the wire spelling.
type GateMode string

const (
	// GateModeVAD transmits on the denoiser's speech probability.
	GateModeVAD GateMode = "vad"
	// GateModePTT transmits while the push-to-talk key is held.
	GateModePTT GateMode = "ptt"
)

// maxHangoverMs mirrors the gate's own bound in internal/audio. It is
// duplicated instead of imported: that package pulls in cgo, and core must
// stay buildable and testable without it.
const maxHangoverMs = 5000

var (
	ErrUnknownGateMode  = errors.New("unknown transmit gate mode")
	ErrInvalidVADTuning = errors.New("invalid vad tuning")
)

// VoiceEngine is what core needs from the audio engine; the adapter in
// main.go binds it to internal/audio and the device layer. Device IDs are
// opaque hex strings ("" = system default). The gate settings live on the
// engine and survive its restarts (device change, device lost); persisting
// them across application restarts is M4 (config.json).
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
func ParseGateMode(mode string) (GateMode, error) {
	switch GateMode(mode) {
	case GateModeVAD:
		return GateModeVAD, nil
	case GateModePTT:
		return GateModePTT, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownGateMode, mode)
}

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
// push-to-talk.
func (a *App) SetGateMode(mode string) error {
	m, err := ParseGateMode(mode)
	if err != nil {
		return err
	}
	if v := a.voiceEngine(); v != nil {
		v.SetGateMode(m)
	}
	return nil
}

// SetVADTuning sets the voice-activation hysteresis band and the hangover
// tail. open is the probability that starts a transmission and close the
// lower edge that keeps it running, so close may not exceed open. The gate
// clamps too, but a request it would have to bend is a UI defect, not a
// setting: report it instead of applying something the user did not ask for.
func (a *App) SetVADTuning(open, close float32, hangoverMs int) error {
	switch {
	case !isProbability(open) || !isProbability(close):
		return fmt.Errorf("%w: thresholds %v/%v outside [0, 1]", ErrInvalidVADTuning, open, close)
	case close > open:
		return fmt.Errorf("%w: close threshold %v above open %v", ErrInvalidVADTuning, close, open)
	case hangoverMs < 0 || hangoverMs > maxHangoverMs:
		return fmt.Errorf("%w: hangover %d ms outside [0, %d]", ErrInvalidVADTuning, hangoverMs, maxHangoverMs)
	}
	if v := a.voiceEngine(); v != nil {
		v.SetVADTuning(open, close, hangoverMs)
	}
	return nil
}

// SetPTT reports whether the push-to-talk key is held. It runs on every key
// transition and stays a plain forward; the engine only flips an atomic.
func (a *App) SetPTT(held bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetPTT(held)
	}
}

// isProbability reports whether v is a usable speech probability. NaN fails
// both comparisons and is rejected.
func isProbability(v float32) bool {
	return v >= 0 && v <= 1
}

// AudioDevices enumerates devices for the settings UI.
func (a *App) AudioDevices() (playback, capture []domain.AudioDevice, err error) {
	v := a.voiceEngine()
	if v == nil {
		return nil, nil, nil
	}
	return v.Devices()
}

// SelectDevices remembers the chosen devices and restarts the engine when
// it is running ("" keeps the system default).
func (a *App) SelectDevices(captureID, playbackID string) {
	a.mu.Lock()
	a.captureID, a.playbackID = captureID, playbackID
	running := a.status.State == domain.StateConnected || a.status.State == domain.StateReconnecting
	a.mu.Unlock()
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
