package core

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/hotkey"
)

// Global push-to-talk (PLAN.md 7 M4).
//
// The monitor watches the configured key system wide exactly when all three
// hold: the gate is in push-to-talk, the user asked for the global key, and
// this machine can watch keys at all. Every other combination leaves the
// monitor stopped and the window-focused listener in the frontend as the only
// source of push-to-talk.
//
// Locking: hotkeyMu serializes the whole start/stop decision and is the outer
// lock. Watch and Stop must never run under a.mu - the callback they wait for
// takes it - so no code path may reach for hotkeyMu while holding a.mu.

// Russian messages for the settings screen. hotkey.Capability explains what
// the platform can do; these explain what went wrong with the key itself.
const (
	hotkeyUnknownKeyMessage  = "Клавиша %s недоступна для глобального режима, выберите другую"
	hotkeyUnavailableMessage = "Клавиша %s не поддерживается системой как глобальная, выберите другую"
	hotkeyUnsupportedMessage = "Глобальная клавиша недоступна в этой системе."
	hotkeyRegisterMessage    = "Не удалось назначить глобальную клавишу %s, возможно её занял другой процесс."
)

// HotkeyStatus is what the settings screen shows about the global key: what
// this machine delivers, why, and what the last attempt to bind the key
// reported. Every text is Russian and ready to render; an empty one means
// there is nothing to say.
type HotkeyStatus struct {
	// Mode is "hold", "toggle" or "unsupported".
	Mode   string
	Reason string
	Error  string
}

// SetKeyMonitor injects the global key monitor and brings the watch in line
// with the stored settings. Call once, after UseSettings.
func (a *App) SetKeyMonitor(m hotkey.Monitor) {
	a.mu.Lock()
	a.keys = m
	a.mu.Unlock()
	a.applyGlobalPTT()
}

// HotkeyStatus reports the global key capability of this machine and the last
// watch error.
func (a *App) HotkeyStatus() HotkeyStatus {
	a.mu.Lock()
	monitor, message := a.keys, a.hotkeyErr
	a.mu.Unlock()

	status := HotkeyStatus{Mode: hotkey.ModeUnsupported.String(), Error: message}
	if monitor != nil {
		capability := monitor.Capability()
		status.Mode, status.Reason = capability.Mode.String(), capability.Reason
	}
	return status
}

// SetGlobalPTT turns the system-wide push-to-talk key on or off and remembers
// the choice. The request is stored even when the key cannot be watched: the
// setting is what the user asked for, and why it is not running is reported
// through HotkeyStatus instead of being silently reverted.
func (a *App) SetGlobalPTT(enabled bool) {
	a.updateSettings(func(c *config.Config) { c.Gate.GlobalPTT = enabled })
	a.applyGlobalPTT()
}

// StopGlobalPTT ends the watch for good. Call on shutdown.
func (a *App) StopGlobalPTT() {
	a.hotkeyMu.Lock()
	defer a.hotkeyMu.Unlock()
	a.stopWatchLocked()
}

// applyGlobalPTT starts, restarts or stops the watch so it matches the stored
// settings. Cheap and idempotent: every mutation that can change the answer
// calls it. Never call it while holding a.mu.
func (a *App) applyGlobalPTT() {
	a.hotkeyMu.Lock()
	defer a.hotkeyMu.Unlock()

	a.mu.Lock()
	monitor, gate := a.keys, a.cfg.Gate
	a.mu.Unlock()
	if monitor == nil {
		return
	}

	if gate.Mode != config.GateModePTT || !gate.GlobalPTT ||
		monitor.Capability().Mode == hotkey.ModeUnsupported {
		a.stopWatchLocked()
		a.reportHotkeyError("")
		return
	}
	if a.watching && a.watchedKey == gate.PTTKey {
		return
	}

	// A key change replaces the running watch; Watch would do it too, but
	// stopping first keeps the release rule in one place.
	a.stopWatchLocked()
	if err := monitor.Watch(gate.PTTKey, a.onHotkey); err != nil {
		a.log.Warn("global push-to-talk", "key", gate.PTTKey, "error", err)
		a.reportHotkeyError(hotkeyErrorMessage(gate.PTTKey, err))
		// Watch leaves the monitor stopped when it fails, and a stopped
		// monitor delivers nothing - not even the release of a key that was
		// held. Nobody else will close the transmission.
		a.SetPTT(false)
		return
	}
	a.watching, a.watchedKey = true, gate.PTTKey
	a.reportHotkeyError("")
	a.log.Info("global push-to-talk watching", "key", gate.PTTKey,
		"mode", monitor.Capability().Mode.String())
}

// stopWatchLocked ends a running watch and closes the transmission it may have
// opened. Caller holds hotkeyMu.
func (a *App) stopWatchLocked() {
	if !a.watching {
		return
	}
	a.mu.Lock()
	monitor := a.keys
	a.mu.Unlock()

	a.watching, a.watchedKey = false, ""
	if monitor != nil {
		monitor.Stop()
	}
	// Stop delivers nothing, so a key still held when the watch ends would
	// leave the microphone open for the rest of the session.
	a.SetPTT(false)
}

// onHotkey is the monitor callback. It runs on the polling goroutine, so it
// stays a plain forward: the engine only flips an atomic.
//
// In toggle mode (Wayland) the monitor latches activations into the same
// held/released alternation, and this side cannot tell the difference - which
// is the point.
func (a *App) onHotkey(held bool) {
	// A panic here would take the process down with it: internal/hotkey does
	// not recover, and the callback runs on its goroutine. Losing the key is
	// survivable, losing the call is not.
	defer func() {
		if r := recover(); r != nil {
			a.log.Error("global push-to-talk callback panicked",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	a.SetPTT(held)
}

// reportHotkeyError stores the message the settings screen shows. Caller holds
// hotkeyMu; a.mu is taken here.
func (a *App) reportHotkeyError(message string) {
	a.mu.Lock()
	a.hotkeyErr = message
	a.mu.Unlock()
}

// hotkeyErrorMessage turns a Watch failure into something the user can act on.
// The key vocabulary of internal/hotkey is narrower than what the settings
// document accepts, so a perfectly valid stored key can still have no global
// form - that is a choice to revisit, not a defect.
func hotkeyErrorMessage(key string, err error) string {
	switch {
	case errors.Is(err, hotkey.ErrUnknownKey):
		return fmt.Sprintf(hotkeyUnknownKeyMessage, key)
	case errors.Is(err, hotkey.ErrKeyUnavailable):
		return fmt.Sprintf(hotkeyUnavailableMessage, key)
	case errors.Is(err, hotkey.ErrUnsupported):
		return hotkeyUnsupportedMessage
	default:
		return fmt.Sprintf(hotkeyRegisterMessage, key)
	}
}
