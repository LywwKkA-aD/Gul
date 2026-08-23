package config

import "math"

// DefaultCueVolume is the shipped gain of the UI cues (join, leave, mute,
// unmute). It matches the engine's own default, so a document without the
// field and a freshly built engine agree.
const DefaultCueVolume = 0.5

// Audio is the audio section of the settings document: the device selection
// as the engine reports device ids - opaque hex strings, empty meaning the
// system default - and the cue gain.
type Audio struct {
	CaptureID  string `json:"capture_id"`
	PlaybackID string `json:"playback_id"`
	// CueVolume is the gain of the UI cues in [0, 1]; 0 turns them off.
	CueVolume float64 `json:"cue_volume"`
}

// defaultAudio returns the audio section of a fresh installation.
func defaultAudio() Audio {
	return Audio{CueVolume: DefaultCueVolume}
}

// ValidCueVolume reports whether v is a usable cue gain. NaN fails both
// comparisons and is rejected.
func ValidCueVolume(v float64) bool { return v >= 0 && v <= 1 }

// sanitized folds a hand-edited section into what the engine accepts. A gain
// outside the range is clamped; one that carries no meaning at all falls back
// to the default. Zero is left alone - it is how the user turns cues off.
func (a Audio) sanitized() Audio {
	switch {
	case math.IsNaN(a.CueVolume):
		a.CueVolume = DefaultCueVolume
	case a.CueVolume < 0:
		a.CueVolume = 0
	case a.CueVolume > 1:
		a.CueVolume = 1
	}
	return a
}
