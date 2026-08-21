//go:build devices

package miniaudio_test

// Live smoke against real audio hardware; excluded from CI by the build
// tag. Run locally: go test -tags devices ./internal/audio/miniaudio/

import (
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/audio/miniaudio"
)

func TestLiveDevices(t *testing.T) {
	ctx, err := miniaudio.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() {
		if err := ctx.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	playback, capture, err := ctx.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	t.Logf("playback devices: %d, capture devices: %d", len(playback), len(capture))
	if len(playback) == 0 || len(capture) == 0 {
		t.Skip("no devices on this machine")
	}

	cap, err := ctx.OpenCapture(nil, 16)
	if err != nil {
		t.Fatalf("OpenCapture: %v", err)
	}
	defer cap.Close()
	pb, err := ctx.OpenPlayback(nil, 16)
	if err != nil {
		t.Fatalf("OpenPlayback: %v", err)
	}
	defer pb.Close()

	t.Logf("capture internal rate %d period %d; playback internal rate %d period %d",
		cap.InternalSampleRate(), cap.InternalPeriod(),
		pb.InternalSampleRate(), pb.InternalPeriod())

	if err := cap.Start(); err != nil {
		t.Fatalf("capture Start: %v", err)
	}
	if err := pb.Start(); err != nil {
		t.Fatalf("playback Start: %v", err)
	}

	// Feed silence and drain the microphone for half a second.
	silence := make([]int16, miniaudio.FrameSamples)
	frame := make([]int16, miniaudio.FrameSamples)
	deadline := time.Now().Add(500 * time.Millisecond)
	reads := 0
	for time.Now().Before(deadline) {
		for cap.ReadFrame(frame) {
			reads++
		}
		pb.WriteFrame(silence)
		time.Sleep(2 * time.Millisecond)
	}

	cs, ps := cap.Stats(), pb.Stats()
	t.Logf("capture: cb_frames=%d ring_dropped=%d reads=%d; playback: cb_frames=%d underruns=%d",
		cs.CallbackFrames, cs.RingDropped, reads, ps.CallbackFrames, ps.Underruns)
	if cs.CallbackFrames == 0 {
		t.Error("capture callback saw no frames")
	}
	if ps.CallbackFrames == 0 {
		t.Error("playback callback saw no frames")
	}
	if reads == 0 {
		t.Error("no frames drained from the capture ring")
	}

	if err := cap.Stop(); err != nil {
		t.Errorf("capture Stop: %v", err)
	}
	if err := pb.Stop(); err != nil {
		t.Errorf("playback Stop: %v", err)
	}
}
