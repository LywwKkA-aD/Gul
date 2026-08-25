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

// ConnectSaved dials a server from the picker: the nickname it was last used
// with and, when one is stored, its password. The password is looked up in
// core and never travels to the UI, so the picker needs nothing but the
// address.
//
// The result says why a click did not start a connect, as a reason the UI can
// switch on: the server is no longer remembered, or its password could not be
// read (a locked credential store), which falls back to the manual form.
// Anything else is an error, like any other failed call.
func (s *ConnectionService) ConnectSaved(address string) (domain.SavedConnect, error) {
	return s.app.ConnectSaved(address)
}

// Servers returns the remembered servers, newest first. HasPassword says
// whether a click on one is enough; the password itself stays in Go.
func (s *ConnectionService) Servers() []domain.SavedServer {
	return s.app.Servers()
}

// ForgetServer drops a server from the picker and its password from the
// credential store. The error reports a password that survived, which is
// exactly what the user asked to be rid of.
func (s *ConnectionService) ForgetServer(address string) error {
	return s.app.ForgetServer(address)
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
