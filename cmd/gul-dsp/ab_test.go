package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/audio"
	"github.com/LywwKkA-aD/Gul/internal/dsp/wav"
)

// speechFixture is the recording the audio package pins its golden tests on;
// one second of it is plenty to exercise the kit.
const speechFixture = "../../internal/audio/testdata/speech_48k.wav"

// abInput writes a short 48 kHz mono WAV into the test directory and returns
// its path.
func abInput(t *testing.T, dir string, samples []int16, rate int) string {
	t.Helper()
	path := filepath.Join(dir, "input.wav")
	if err := wav.Write(path, samples, rate); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func abFixture(t *testing.T) []int16 {
	t.Helper()
	samples, rate, err := wav.Read(speechFixture)
	if err != nil {
		t.Fatalf("read %s: %v", speechFixture, err)
	}
	if rate != audio.SampleRate {
		t.Fatalf("%s is %d Hz, the grid is %d Hz", speechFixture, rate, audio.SampleRate)
	}
	return samples[:audio.SampleRate]
}

// TestABRoundTrip runs the kit end to end: both samples come out on the
// project grid, at the length of the input, and the key names each
// candidate exactly once.
func TestABRoundTrip(t *testing.T) {
	dir := t.TempDir()
	input := abFixture(t)
	inPath := abInput(t, dir, input, audio.SampleRate)
	outDir := filepath.Join(dir, "out")

	if err := runAB([]string{"-in", inPath, "-out", outDir}); err != nil {
		t.Fatalf("runAB: %v", err)
	}

	var outputs [][]int16
	for _, name := range []string{"sample-1.wav", "sample-2.wav"} {
		got, rate, err := wav.Read(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if rate != audio.SampleRate {
			t.Errorf("%s is %d Hz, want %d", name, rate, audio.SampleRate)
		}
		if len(got) != len(input) {
			t.Errorf("%s holds %d samples, want %d", name, len(got), len(input))
		}
		if audio.RMS(got) < 100 {
			t.Errorf("%s came out at RMS %.1f - the processed audio is silent",
				name, audio.RMS(got))
		}
		outputs = append(outputs, got)
	}
	// The whole point of the kit is that the two files carry two different
	// chains: identical outputs would mean one candidate was processed
	// twice while the key still names both.
	if len(outputs) == 2 && slices.Equal(outputs[0], outputs[1]) {
		t.Error("sample-1.wav and sample-2.wav are identical")
	}

	key, err := os.ReadFile(filepath.Join(outDir, keyFile))
	if err != nil {
		t.Fatalf("read %s: %v", keyFile, err)
	}
	text := string(key)
	for _, c := range candidates {
		if n := strings.Count(text, c.desc); n != 1 {
			t.Errorf("key names %q %d times, want exactly once", c.desc, n)
		}
	}
	for _, name := range []string{"sample-1.wav", "sample-2.wav"} {
		if !strings.Contains(text, name) {
			t.Errorf("key does not mention %s", name)
		}
	}
}

// TestABRejectsForeignFormats pins the guard against the grid: anything but
// 48 kHz mono int16 has to fail with the expected format in the message,
// because the chain silently interprets whatever it is given as 48 kHz.
func TestABRejectsForeignFormats(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")

	t.Run("wrong rate", func(t *testing.T) {
		samples := make([]int16, 16000)
		copy(samples, abFixture(t))
		path := abInput(t, t.TempDir(), samples, 16000)
		err := runAB([]string{"-in", path, "-out", outDir})
		if err == nil {
			t.Fatal("a 16 kHz file was accepted")
		}
		if !strings.Contains(err.Error(), "48000") {
			t.Errorf("error %q does not name the expected sample rate", err)
		}
	})

	t.Run("too short", func(t *testing.T) {
		path := abInput(t, t.TempDir(), make([]int16, 100), audio.SampleRate)
		if err := runAB([]string{"-in", path, "-out", outDir}); err == nil {
			t.Fatal("a file shorter than one frame was accepted")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(dir, "nothing.wav")
		if err := runAB([]string{"-in", path, "-out", outDir}); err == nil {
			t.Fatal("a missing file was accepted")
		}
	})

	t.Run("missing arguments", func(t *testing.T) {
		if err := runAB(nil); err == nil {
			t.Fatal("the command ran without -in and -out")
		}
	})

	if _, err := os.Stat(outDir); err == nil {
		t.Error("the output directory was created for a rejected input")
	}
}

// TestRunDispatch pins the subcommand surface: an unknown command fails
// instead of doing something surprising, and help is not an error.
func TestRunDispatch(t *testing.T) {
	if err := run([]string{"nope"}); err == nil {
		t.Error("an unknown command was accepted")
	}
	if err := run(nil); err == nil {
		t.Error("an empty command line was accepted")
	}
	if err := run([]string{"help"}); err != nil {
		t.Errorf("help: %v", err)
	}
}
