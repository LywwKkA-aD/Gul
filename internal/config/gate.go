package config

import (
	"errors"
	"fmt"
	"math"
	"unicode"
)

// GateMode selects what opens the microphone. The spellings are the ones that
// cross the UI boundary and the ones written to disk, so they live here with
// the rest of the persisted vocabulary; core re-exports them.
type GateMode string

const (
	// GateModeVAD transmits on the denoiser's speech probability.
	GateModeVAD GateMode = "vad"
	// GateModePTT transmits while the push-to-talk key is held.
	GateModePTT GateMode = "ptt"
)

var ErrUnknownGateMode = errors.New("unknown transmit gate mode")

const (
	// DefaultOpenThreshold and DefaultHangoverMs are the gate defaults of
	// PLAN.md 4.3, so a document without them matches a freshly built gate.
	DefaultOpenThreshold = 0.6
	DefaultHangoverMs    = 300

	// MaxHangoverMs mirrors the gate's own bound in internal/audio. That
	// package pulls in cgo, so its constant cannot be imported here; this is
	// the one place the bound is written for everything above the engine.
	MaxHangoverMs = 5000

	// DefaultPTTKey is a KeyboardEvent.code: a physical key, so the binding
	// does not move when the keyboard layout does.
	DefaultPTTKey = "Space"

	// maxPTTKeyLen bounds what may reach a hotkey registration. Every
	// KeyboardEvent.code is a short alphanumeric name ("Space", "KeyA",
	// "IntlBackslash"); anything else is not a key this app ever wrote.
	maxPTTKeyLen = 32

	// hysteresisBand is the width between the opening and the closing edge of
	// the VAD gate (internal/audio: open 0.6, close 0.4). Only the opening
	// edge is a user decision; the band is a property of the gate.
	hysteresisBand = 0.2

	// closeFloor keeps the closing edge above zero: a gate that only closes
	// on absolute silence never closes at all.
	closeFloor = 0.05
)

// Gate is the transmit gate section of the settings document.
type Gate struct {
	Mode          GateMode `json:"mode"`
	OpenThreshold float64  `json:"open_threshold"`
	HangoverMs    int      `json:"hangover_ms"`
	PTTKey        string   `json:"ptt_key"`
	// GlobalPTT asks for the key to be watched system wide. It only has an
	// effect in Mode ptt and on a machine whose monitor can watch keys at
	// all; core keeps the watch in line with both (PLAN.md 7 M4).
	GlobalPTT bool `json:"global_ptt"`
}

// defaultGate returns the gate section of a fresh installation.
func defaultGate() Gate {
	return Gate{
		Mode:          GateModeVAD,
		OpenThreshold: DefaultOpenThreshold,
		HangoverMs:    DefaultHangoverMs,
		PTTKey:        DefaultPTTKey,
	}
}

// ParseGateMode validates the mode spelling that crosses the UI boundary.
// An unknown mode is an error rather than a fallback: answering a request for
// push-to-talk with voice activation leaves the microphone open.
func ParseGateMode(mode string) (GateMode, error) {
	switch GateMode(mode) {
	case GateModeVAD:
		return GateModeVAD, nil
	case GateModePTT:
		return GateModePTT, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownGateMode, mode)
}

// ValidOpenThreshold reports whether v is a usable speech probability.
// NaN fails both comparisons and is rejected.
func ValidOpenThreshold(v float64) bool { return v >= 0 && v <= 1 }

// ValidHangoverMs reports whether ms is a tail the gate accepts.
func ValidHangoverMs(ms int) bool { return ms >= 0 && ms <= MaxHangoverMs }

// ValidPTTKey reports whether code looks like a KeyboardEvent.code. Those are
// alphanumeric ASCII names; anything else is rejected instead of stored.
func ValidPTTKey(code string) bool {
	if code == "" || len(code) > maxPTTKeyLen {
		return false
	}
	for _, r := range code {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// CloseThreshold derives the closing edge of the hysteresis band from the
// opening one, rounded to whole percent so the value carries no float noise.
// The result never exceeds open, which keeps a hand-edited threshold below
// the floor from inverting the band.
func CloseThreshold(open float64) float64 {
	if !ValidOpenThreshold(open) {
		open = DefaultOpenThreshold
	}
	closed := math.Round((open-hysteresisBand)*100) / 100
	if closed < closeFloor {
		closed = closeFloor
	}
	if closed > open {
		closed = open
	}
	return closed
}

// sanitized folds a hand-edited or migrated section into what the engine
// accepts. Out-of-range numbers are clamped; a value that carries no meaning
// at all (unknown mode, unusable key, NaN) falls back to the default.
func (g Gate) sanitized() Gate {
	if _, err := ParseGateMode(string(g.Mode)); err != nil {
		g.Mode = GateModeVAD
	}
	switch {
	case math.IsNaN(g.OpenThreshold):
		g.OpenThreshold = DefaultOpenThreshold
	case g.OpenThreshold < 0:
		g.OpenThreshold = 0
	case g.OpenThreshold > 1:
		g.OpenThreshold = 1
	}
	switch {
	case g.HangoverMs < 0:
		g.HangoverMs = 0
	case g.HangoverMs > MaxHangoverMs:
		g.HangoverMs = MaxHangoverMs
	}
	if !ValidPTTKey(g.PTTKey) {
		g.PTTKey = DefaultPTTKey
	}
	return g
}
