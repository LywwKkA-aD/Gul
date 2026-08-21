package core

import (
	"fmt"
	"log/slog"
	"sync"

	"gul/internal/mumble"
)

// App owns the application state and orchestrates the layers beneath the
// Wails services. Services stay thin and delegate here.
type App struct {
	mu      sync.Mutex
	log     *slog.Logger
	tofu    *mumble.TOFUStore
	session *mumble.Session
}

func New(log *slog.Logger, tofu *mumble.TOFUStore) *App {
	return &App{log: log, tofu: tofu}
}

func (a *App) Connect(address, username, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.session != nil {
		if err := a.session.Disconnect(); err != nil {
			a.log.Warn("disconnect before reconnect", "error", err)
		}
		a.session = nil
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}

	session, err := mumble.Dial(mumble.DialConfig{
		Address:  address,
		Username: username,
		Password: password,
	}, a.tofu, a.log)
	if err != nil {
		return err
	}
	a.session = session
	return nil
}

func (a *App) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return nil
	}
	err := a.session.Disconnect()
	a.session = nil
	return err
}

func (a *App) State() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return "disconnected"
	}
	return a.session.State()
}
