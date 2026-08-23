package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
)

// SettingsService is the thin Wails bridge for the persisted settings.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
//
// What the transmit gate does - mode, thresholds, devices - keeps going
// through AudioService, because applying it to the engine and remembering it
// is one operation in core and a second path would be a second source of
// truth. What the settings screen decides around it lives here: the
// push-to-talk binding, whether it is watched system wide, and the loudness
// of the UI cues.
type SettingsService struct {
	app *core.App
}

func NewSettingsService(app *core.App) *SettingsService {
	return &SettingsService{app: app}
}

// Settings is the persisted configuration as the UI reads it: flat, because
// the store is flat, and camelCase, because the on-disk spelling is the
// config package's business. The server password is not part of it.
type Settings struct {
	Address    string  `json:"address"`
	Username   string  `json:"username"`
	CaptureID  string  `json:"captureId"`
	PlaybackID string  `json:"playbackId"`
	GateMode   string  `json:"gateMode"`
	VadOpen    float64 `json:"vadOpen"`
	HangoverMs int     `json:"hangoverMs"`
	PttKey     string  `json:"pttKey"`
	GlobalPtt  bool    `json:"globalPtt"`
	CueVolume  float64 `json:"cueVolume"`
	// Hotkey is runtime state, not a stored setting: it says what this
	// machine can do with the key that is stored.
	Hotkey Hotkey `json:"hotkey"`
}

// Hotkey describes the global push-to-talk key on this machine. Mode is
// "hold" (the key is watched and released properly), "toggle" (the platform
// only reports activations, so one opens the microphone and the next closes
// it) or "unsupported". Reason and Error are Russian and ready to render:
// Reason explains a mode that is not plain hold-to-talk, Error reports the
// last attempt to bind the stored key. Both are empty when there is nothing
// to say.
type Hotkey struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

// Load returns the settings snapshot the UI starts on.
func (s *SettingsService) Load() Settings {
	cfg := s.app.Settings()
	keys := s.app.HotkeyStatus()
	return Settings{
		Address:    cfg.Connection.LastAddress,
		Username:   cfg.Connection.LastUsername,
		CaptureID:  cfg.Audio.CaptureID,
		PlaybackID: cfg.Audio.PlaybackID,
		GateMode:   string(cfg.Gate.Mode),
		VadOpen:    cfg.Gate.OpenThreshold,
		HangoverMs: cfg.Gate.HangoverMs,
		PttKey:     cfg.Gate.PTTKey,
		GlobalPtt:  cfg.Gate.GlobalPTT,
		CueVolume:  cfg.Audio.CueVolume,
		Hotkey: Hotkey{
			Mode:   keys.Mode,
			Reason: keys.Reason,
			Error:  keys.Error,
		},
	}
}

// SetPTTKey stores the push-to-talk binding as a KeyboardEvent.code.
func (s *SettingsService) SetPTTKey(code string) error {
	return s.app.SetPTTKey(code)
}

// SetGlobalPTT turns the system-wide push-to-talk key on or off. The result is
// not an error even when the key cannot be watched: what this machine can do
// and what the last attempt reported are in Load().Hotkey.
func (s *SettingsService) SetGlobalPTT(enabled bool) {
	s.app.SetGlobalPTT(enabled)
}

// SetCueVolume sets the loudness of the join, leave and mute sounds. The gain
// is in [0, 1]; 0 turns them off.
func (s *SettingsService) SetCueVolume(volume float64) error {
	return s.app.SetCueVolume(volume)
}
