package services

import (
	"gul/internal/core"
	"gul/internal/domain"
)

// AudioService bridges the UI to the voice engine through core: mute and
// deafen, per-user volumes, device selection. Thin by design.
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
