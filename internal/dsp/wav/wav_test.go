package wav_test

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/dsp/wav"
)

func TestRoundTrip(t *testing.T) {
	samples := make([]int16, 4800)
	for i := range samples {
		samples[i] = int16(9000 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := wav.Write(path, samples, 48000); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, rate, err := wav.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rate != 48000 {
		t.Fatalf("rate = %d, want 48000", rate)
	}
	if len(got) != len(samples) {
		t.Fatalf("length = %d, want %d", len(got), len(samples))
	}
	for i := range got {
		if got[i] != samples[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], samples[i])
		}
	}
}

func TestReadFixture(t *testing.T) {
	// The committed speech fixture is the contract of several DSP tests:
	// pin its shape here so a re-generated file cannot silently change it.
	samples, rate, err := wav.Read("../../audio/testdata/speech_48k.wav")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rate != 48000 {
		t.Fatalf("rate = %d, want 48000", rate)
	}
	if len(samples) < 3*48000 {
		t.Fatalf("fixture holds %d samples, want at least 3 s of speech", len(samples))
	}
	var peak int16
	for _, s := range samples {
		if s > peak {
			peak = s
		}
	}
	if peak < 8000 {
		t.Fatalf("fixture peak %d, want a healthy speech level", peak)
	}
}

func TestReadRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.wav")
	if err := wav.Write(path, []int16{1, 2, 3}, 48000); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, _, err := wav.Read(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Fatal("Read of a missing file succeeded")
	}
}
