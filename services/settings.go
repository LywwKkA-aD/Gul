package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
)

// SettingsService is the thin Wails bridge for the persisted settings.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
//
// Only the settings without another home are mutated through this service.
// Devices and the transmit gate keep going through AudioService, because
// applying them to the engine and remembering them is one operation in core;
// a second path would be a second source of truth.
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
}

// Load returns the settings snapshot the UI starts on.
func (s *SettingsService) Load() Settings {
	cfg := s.app.Settings()
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
	}
}

// SetPTTKey stores the push-to-talk binding as a KeyboardEvent.code.
func (s *SettingsService) SetPTTKey(code string) error {
	return s.app.SetPTTKey(code)
}
