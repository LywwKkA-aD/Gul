package services

import "gul/internal/core"

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
	return s.app.Disconnect()
}

func (s *ConnectionService) State() string {
	return s.app.State()
}
