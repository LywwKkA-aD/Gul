package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// AudioService bridges the UI to the voice engine through core: mute and
// deafen, per-user volumes, device selection, transmit gate. Thin by design.
type AudioService struct {
	app *core.App
}

func NewAudioService(app *core.App) *AudioService {
	return &AudioService{app: app}
}

// SetMute gates the microphone.
func (s *AudioService) SetMute(muted bool) {
	s.app.SetMute(muted)
}

// SetDeafen silences all remote streams locally.
func (s *AudioService) SetDeafen(deafened bool) {
	s.app.SetDeafen(deafened)
}

// SetUserVolume sets a per-user gain (1.0 = unity) keyed by the stable
// certificate hash.
func (s *AudioService) SetUserVolume(hash string, volume float64) {
	s.app.SetUserVolume(hash, float32(volume))
}

// SetGateMode selects what opens the microphone: "vad" (voice activation) or
// "ptt" (push-to-talk).
func (s *AudioService) SetGateMode(mode string) error {
	return s.app.SetGateMode(mode)
}

// SetVADTuning sets the voice-activation threshold (a speech probability in
// [0, 1]) and the hangover tail in milliseconds. open is float64 because that
// is what the bindings marshal a JS number into; the closing edge of the
// hysteresis band is derived in core rather than asked of the user.
func (s *AudioService) SetVADTuning(open float64, hangoverMs int) error {
	return s.app.SetVADTuning(open, hangoverMs)
}

// SetPTT reports the push-to-talk key going down or up. Only the "ptt" gate
// mode acts on it.
func (s *AudioService) SetPTT(held bool) error {
	s.app.SetPTT(held)
	return nil
}

// AudioDevices is the device enumeration for the settings UI.
type AudioDevices struct {
	Playback []domain.AudioDevice `json:"playback"`
	Capture  []domain.AudioDevice `json:"capture"`
}

// Devices lists selectable playback and capture devices.
func (s *AudioService) Devices() (AudioDevices, error) {
	playback, capture, err := s.app.AudioDevices()
	if err != nil {
		return AudioDevices{}, err
	}
	return AudioDevices{Playback: playback, Capture: capture}, nil
}

// SelectDevices applies the chosen devices ("" = system default) and
// restarts the engine when it is running.
func (s *AudioService) SelectDevices(captureID, playbackID string) {
	s.app.SelectDevices(captureID, playbackID)
}
