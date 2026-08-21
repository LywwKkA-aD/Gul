package mumble

import (
	"fmt"
	"log/slog"

	"gul/internal/domain"
)

// Manager owns the connection lifecycle: dialing, reconnect with backoff,
// channel restore, client certificate identity and TOFU decisions.
//
// STUB: compilable contract skeleton; the real implementation replaces the
// method bodies (M1 mumble task).
type Manager struct {
	log *slog.Logger
	cb  Callbacks
}

// NewManager loads the TOFU store and the client certificate (generating it
// on first run) from cfgDir and returns a ready-to-use Manager.
func NewManager(cfgDir string, log *slog.Logger, cb Callbacks) (*Manager, error) {
	return &Manager{log: log, cb: cb}, nil
}

func (m *Manager) Connect(address, username, password string) {
	if m.cb.OnStatus != nil {
		m.cb.OnStatus(domain.ConnectionStatus{State: domain.StateDisconnected, Server: address, Error: "not implemented"})
	}
}

func (m *Manager) Disconnect() {}

func (m *Manager) Join(channelID uint32) error {
	return fmt.Errorf("not implemented")
}

func (m *Manager) SendMessage(channelID uint32, text string) error {
	return fmt.Errorf("not implemented")
}

func (m *Manager) AcceptFingerprint() {}

func (m *Manager) Status() domain.ConnectionStatus {
	return domain.ConnectionStatus{State: domain.StateDisconnected}
}

func (m *Manager) Close() {}
