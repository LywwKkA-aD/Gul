package core

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/config"
)

// Persisted settings (PLAN.md 7 M4). The App owns the document: services
// mutate it through typed methods, every mutation is validated here, and the
// write is debounced so a slider drag costs one file write, not one per pixel.

// settingsSaveWindow is how long a change waits for the ones behind it.
const settingsSaveWindow = 500 * time.Millisecond

var ErrInvalidPTTKey = errors.New("invalid push-to-talk key")

// UseSettings adopts the configuration loaded from dir and applies it.
//
// loadErr is what config.Load reported. It is logged here, and a document this
// build must not overwrite (written by a newer version) also turns persistence
// off for the session: the settings then live in memory only and the file
// keeps what wrote it.
//
// Call after SetVoice and before the first Connect - the device selection has
// to be in place before the engine starts, and the gate settings have to reach
// an engine that already exists.
func (a *App) UseSettings(dir string, cfg config.Config, loadErr error) {
	if loadErr != nil {
		a.log.Warn("settings", "error", loadErr)
	}
	cfg = cfg.Sanitized()

	a.mu.Lock()
	a.cfgDir = dir
	a.cfg = cfg
	a.persistSettings = dir != "" &&
		!errors.Is(loadErr, config.ErrUnsupportedVersion) &&
		!errors.Is(loadErr, config.ErrUnreadable)
	a.captureID, a.playbackID = cfg.Audio.CaptureID, cfg.Audio.PlaybackID
	voice := a.voice
	a.mu.Unlock()

	applyGate(voice, cfg.Gate)
}

// Settings returns the current configuration snapshot for the UI.
func (a *App) Settings() config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// SetPTTKey stores the push-to-talk binding. The value is a
// KeyboardEvent.code - a physical key, so it does not move with the layout -
// and it is only ever read by the UI listener until the global shortcut lands.
func (a *App) SetPTTKey(code string) error {
	if !config.ValidPTTKey(code) {
		return fmt.Errorf("%w: %q", ErrInvalidPTTKey, code)
	}
	a.updateSettings(func(c *config.Config) { c.Gate.PTTKey = code })
	return nil
}

// RememberConnection records what the connect form should start on next time.
// Called once the server has accepted the credentials, never on the attempt:
// a typo or a dead host must not be what comes back. The password is not part
// of it and is never persisted.
func (a *App) RememberConnection(address, username string) {
	address, username = strings.TrimSpace(address), strings.TrimSpace(username)
	if address == "" || username == "" {
		return
	}
	a.updateSettings(func(c *config.Config) {
		c.Connection.LastAddress = address
		c.Connection.LastUsername = username
	})
}

// commitConnection remembers the attempt the server has just accepted.
func (a *App) commitConnection() {
	a.mu.Lock()
	address, username := a.address, a.username
	a.mu.Unlock()
	a.RememberConnection(address, username)
}

// FlushSettings writes a pending change immediately. Call on shutdown: the
// debounce window is short, but a setting changed inside it would otherwise
// be lost with the process.
func (a *App) FlushSettings() {
	a.saver.flush()
}

// updateSettings applies fn to the owned configuration and schedules a write
// if it changed anything. The sections are compared instead of the document
// as a whole: Config carries the fields of the file this build does not know,
// and those are never what a mutation touches.
func (a *App) updateSettings(fn func(*config.Config)) {
	a.mu.Lock()
	before := a.cfg
	fn(&a.cfg)
	a.cfg = a.cfg.Sanitized()
	changed := a.cfg.Connection != before.Connection ||
		a.cfg.Audio != before.Audio ||
		a.cfg.Gate != before.Gate
	a.mu.Unlock()

	if changed {
		a.saver.schedule()
	}
}

// saveSettings writes the current snapshot. A configuration directory that
// cannot be written costs persistence for the session and nothing else: the
// settings keep working in memory, and the warning is logged once rather than
// on every change (the TOFU store makes the same trade).
func (a *App) saveSettings() {
	a.mu.Lock()
	dir, cfg, persist := a.cfgDir, a.cfg, a.persistSettings
	a.mu.Unlock()
	if !persist {
		return
	}
	if err := config.Save(dir, cfg); err != nil {
		a.mu.Lock()
		a.persistSettings = false
		a.mu.Unlock()
		a.log.Warn("settings not writable, changes are session-scoped",
			"file", config.FileName, "error", err)
	}
}

// ----------------------------------------------------------------------------
// Debounced writer
// ----------------------------------------------------------------------------

// oneShotTimer is the part of time.Timer the writer needs. It is an interface
// so tests can close the window on demand instead of waiting for it.
type oneShotTimer interface{ Stop() bool }

// afterFunc arms a one-shot timer; production passes time.AfterFunc.
type afterFunc func(time.Duration, func()) oneShotTimer

func realAfterFunc(d time.Duration, f func()) oneShotTimer { return time.AfterFunc(d, f) }

// settingsSaver coalesces a burst of changes into a single write. The first
// change opens the window and every change inside it rides along, so a write
// happens at most once per window and never later than one window after the
// change that asked for it.
type settingsSaver struct {
	window time.Duration
	after  afterFunc
	write  func()

	mu    sync.Mutex
	armed oneShotTimer

	// writeMu keeps two writes from interleaving, so the last snapshot to
	// start writing is also the last one to reach the file.
	writeMu sync.Mutex
}

func newSettingsSaver(window time.Duration, write func()) *settingsSaver {
	return &settingsSaver{window: window, after: realAfterFunc, write: write}
}

func (s *settingsSaver) schedule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.armed != nil {
		return
	}
	s.armed = s.after(s.window, s.fire)
}

func (s *settingsSaver) fire() {
	s.mu.Lock()
	s.armed = nil
	s.mu.Unlock()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.write()
}

// flush disarms the window and writes, whether or not anything is pending:
// on the way out, one redundant write is cheaper than reasoning about a timer
// that may be running right now.
func (s *settingsSaver) flush() {
	s.mu.Lock()
	if s.armed != nil {
		s.armed.Stop()
		s.armed = nil
	}
	s.mu.Unlock()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.write()
}
