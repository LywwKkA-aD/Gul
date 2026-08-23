package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCueVolumeDefaultMatchesTheEngine(t *testing.T) {
	t.Parallel()
	// internal/audio ships cueVolumeDefault = 0.5. The document and a freshly
	// built engine have to describe the same loudness, or a first start would
	// retune the cues it just created.
	if DefaultCueVolume != 0.5 {
		t.Errorf("DefaultCueVolume = %v, want 0.5", DefaultCueVolume)
	}
	if got := Defaults().Audio.CueVolume; got != DefaultCueVolume {
		t.Errorf("Defaults().Audio.CueVolume = %v, want %v", got, DefaultCueVolume)
	}
}

func TestValidCueVolume(t *testing.T) {
	t.Parallel()
	for _, v := range []float64{0, 0.5, 1} {
		if !ValidCueVolume(v) {
			t.Errorf("ValidCueVolume(%v) = false", v)
		}
	}
	for _, v := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		if ValidCueVolume(v) {
			t.Errorf("ValidCueVolume(%v) = true", v)
		}
	}
}

func TestAudioSanitizedClampsTheCueVolume(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want float64
	}{
		{-1, 0},
		// Zero carries a meaning of its own: cues off.
		{0, 0},
		{0.25, 0.25},
		{1, 1},
		{4, 1},
		{math.NaN(), DefaultCueVolume},
	}
	for _, c := range cases {
		if got := (Audio{CueVolume: c.in}).sanitized().CueVolume; got != c.want {
			t.Errorf("Audio{%v}.sanitized() = %v, want %v", c.in, got, c.want)
		}
	}
}

// A document written before the field existed must not silence the cues: the
// decoder merges over Defaults(), so an absent key keeps the shipped gain.
func TestLoadFillsAMissingCueVolume(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	doc := `{"version": 1, "audio": {"capture_id": "aa11"}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Audio.CueVolume != DefaultCueVolume {
		t.Errorf("CueVolume = %v, want %v", cfg.Audio.CueVolume, DefaultCueVolume)
	}
	if cfg.Audio.CaptureID != "aa11" {
		t.Errorf("CaptureID = %q, want it preserved", cfg.Audio.CaptureID)
	}
}

func TestCueVolumeSurvivesARoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Audio.CueVolume = 0
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Audio.CueVolume != 0 {
		t.Errorf("CueVolume = %v, want 0 - cues off has to survive the file",
			loaded.Audio.CueVolume)
	}
}
