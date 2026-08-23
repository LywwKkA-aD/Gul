package config

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParseGateMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"vad", "ptt"} {
		got, err := ParseGateMode(mode)
		if err != nil || string(got) != mode {
			t.Errorf("ParseGateMode(%q) = %q, %v", mode, got, err)
		}
	}
	// Answering "push-to-talk" with voice activation would leave the
	// microphone open, so an unknown spelling has no fallback.
	if _, err := ParseGateMode("whisper"); !errors.Is(err, ErrUnknownGateMode) {
		t.Errorf("err = %v, want %v", err, ErrUnknownGateMode)
	}
}

// The defaults of the document and of a freshly built gate (internal/audio:
// open 0.6, close 0.4, 300 ms) have to describe the same gate, or a first
// start would silently retune the one it just created.
func TestGateDefaultsMatchTheEngine(t *testing.T) {
	t.Parallel()
	if got := CloseThreshold(DefaultOpenThreshold); got != 0.4 {
		t.Errorf("CloseThreshold(%v) = %v, want 0.4", DefaultOpenThreshold, got)
	}
	if DefaultHangoverMs != 300 {
		t.Errorf("DefaultHangoverMs = %d, want 300", DefaultHangoverMs)
	}
}

func TestCloseThreshold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		open, want float64
	}{
		{0.95, 0.75},
		{0.6, 0.4},
		{0.31, 0.11},
		{0.2, closeFloor},
		// Below the floor the band would invert, which the gate reads as a
		// threshold that never closes.
		{0.03, 0.03},
		{0, 0},
	}
	for _, c := range cases {
		if got := CloseThreshold(c.open); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("CloseThreshold(%v) = %v, want %v", c.open, got, c.want)
		}
		if got := CloseThreshold(c.open); got > c.open {
			t.Errorf("CloseThreshold(%v) = %v, above the opening edge", c.open, got)
		}
	}
	if got := CloseThreshold(math.NaN()); got != CloseThreshold(DefaultOpenThreshold) {
		t.Errorf("CloseThreshold(NaN) = %v, want the default band", got)
	}
}

func TestValidOpenThreshold(t *testing.T) {
	t.Parallel()
	for _, v := range []float64{0, 0.5, 1} {
		if !ValidOpenThreshold(v) {
			t.Errorf("ValidOpenThreshold(%v) = false", v)
		}
	}
	for _, v := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		if ValidOpenThreshold(v) {
			t.Errorf("ValidOpenThreshold(%v) = true", v)
		}
	}
}

func TestValidHangoverMs(t *testing.T) {
	t.Parallel()
	for _, ms := range []int{0, DefaultHangoverMs, MaxHangoverMs} {
		if !ValidHangoverMs(ms) {
			t.Errorf("ValidHangoverMs(%d) = false", ms)
		}
	}
	for _, ms := range []int{-1, MaxHangoverMs + 1} {
		if ValidHangoverMs(ms) {
			t.Errorf("ValidHangoverMs(%d) = true", ms)
		}
	}
}

func TestValidPTTKey(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"Space", "KeyA", "Digit1", "F13", "IntlBackslash", "NumpadEnter"} {
		if !ValidPTTKey(code) {
			t.Errorf("ValidPTTKey(%q) = false", code)
		}
	}
	// Whatever ends up here may be handed to an OS hotkey registration.
	for _, code := range []string{"", " ", "Ctrl+A", "Пробел", strings.Repeat("K", maxPTTKeyLen+1)} {
		if ValidPTTKey(code) {
			t.Errorf("ValidPTTKey(%q) = true", code)
		}
	}
}

func TestGateSanitizedClampsInsteadOfRejecting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Gate
		want Gate
	}{
		{
			name: "unknown mode falls back to voice activation",
			in:   Gate{Mode: "shout", OpenThreshold: 0.5, HangoverMs: 200, PTTKey: "KeyF"},
			want: Gate{Mode: GateModeVAD, OpenThreshold: 0.5, HangoverMs: 200, PTTKey: "KeyF"},
		},
		{
			name: "thresholds fold into the probability range",
			in:   Gate{Mode: GateModeVAD, OpenThreshold: -2, HangoverMs: -5, PTTKey: "Space"},
			want: Gate{Mode: GateModeVAD, OpenThreshold: 0, HangoverMs: 0, PTTKey: "Space"},
		},
		{
			name: "a tail of minutes folds onto the bound",
			in:   Gate{Mode: GateModePTT, OpenThreshold: 2, HangoverMs: 60000, PTTKey: "Space"},
			want: Gate{Mode: GateModePTT, OpenThreshold: 1, HangoverMs: MaxHangoverMs, PTTKey: "Space"},
		},
		{
			name: "a value that means nothing falls back to the default",
			in:   Gate{Mode: GateModeVAD, OpenThreshold: math.NaN(), HangoverMs: 300, PTTKey: "Ctrl+Q"},
			want: defaultGate(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.in.sanitized(); got != c.want {
				t.Errorf("sanitized() = %+v, want %+v", got, c.want)
			}
		})
	}
}
