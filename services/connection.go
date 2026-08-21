package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// ConnectionService is the thin Wails bridge for connection control.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
type ConnectionService struct {
	app *core.App
}

func NewConnectionService(app *core.App) *ConnectionService {
	return &ConnectionService{app: app}
}

func (s *ConnectionService) Connect(address, username, password string) error {
	return s.app.Connect(address, username, password)
}

func (s *ConnectionService) Disconnect() error {
	s.app.Disconnect()
	return nil
}

// State returns just the lifecycle phase; kept for the M0 binding signature.
func (s *ConnectionService) State() string {
	return string(s.app.Status().State)
}

// Status returns the full snapshot the UI needs on mount.
func (s *ConnectionService) Status() domain.ConnectionStatus {
	return s.app.Status()
}

// AcceptFingerprint confirms a pending TOFU mismatch.
func (s *ConnectionService) AcceptFingerprint() {
	s.app.AcceptFingerprint()
}
