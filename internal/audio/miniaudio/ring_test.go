package miniaudio_test

import (
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/audio/miniaudio"
)

// pattern fills a frame with a per-frame recognizable sequence.
func pattern(frame []int16, seq int) {
	for i := range frame {
		frame[i] = int16(seq*31 + i)
	}
}

func TestRingRejectsBadSizes(t *testing.T) {
	t.Parallel()
	if _, err := miniaudio.NewRing(0, 4); err == nil {
		t.Fatal("NewRing(0, 4) succeeded, want error")
	}
	if _, err := miniaudio.NewRing(480, 0); err == nil {
		t.Fatal("NewRing(480, 0) succeeded, want error")
	}
}

func TestRingOrderAndContent(t *testing.T) {
	t.Parallel()
	ring, err := miniaudio.NewRing(8, 4)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	defer ring.Close()

	frame := make([]int16, 8)
	for seq := range 3 {
		pattern(frame, seq)
		if !ring.Push(frame) {
			t.Fatalf("Push %d failed", seq)
		}
	}
	if got := ring.Available(); got != 3 {
		t.Fatalf("Available = %d, want 3", got)
	}

	want := make([]int16, 8)
	got := make([]int16, 8)
	for seq := range 3 {
		if !ring.Pop(got) {
			t.Fatalf("Pop %d failed", seq)
		}
		pattern(want, seq)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("frame %d sample %d = %d, want %d", seq, i, got[i], want[i])
			}
		}
	}
	if ring.Pop(got) {
		t.Fatal("Pop on empty ring succeeded")
	}
}

func TestRingOverflowDropsNewest(t *testing.T) {
	t.Parallel()
	ring, err := miniaudio.NewRing(4, 2)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	defer ring.Close()

	frame := make([]int16, 4)
	for seq := range 5 {
		pattern(frame, seq)
		ring.Push(frame)
	}
	if got := ring.Available(); got != 2 {
		t.Fatalf("Available = %d, want 2", got)
	}
	if got := ring.Dropped(); got != 3 {
		t.Fatalf("Dropped = %d, want 3", got)
	}
	// The survivors are the oldest frames: 0 and 1.
	got := make([]int16, 4)
	if !ring.Pop(got) || got[0] != 0 {
		t.Fatalf("first survivor starts with %d, want 0", got[0])
	}
}

func TestRingWrongFrameLength(t *testing.T) {
	t.Parallel()
	ring, err := miniaudio.NewRing(8, 2)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	defer ring.Close()
	if ring.Push(make([]int16, 7)) {
		t.Fatal("Push accepted a short frame")
	}
	if ring.Pop(make([]int16, 9)) {
		t.Fatal("Pop accepted a long frame")
	}
}

// TestRingSPSC drives the ring from two goroutines the way the audio
// callback and the DSP goroutine will: order and content must survive.
func TestRingSPSC(t *testing.T) {
	t.Parallel()
	const frames = 5000
	ring, err := miniaudio.NewRing(16, 8)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	defer ring.Close()

	go func() {
		frame := make([]int16, 16)
		for seq := 0; seq < frames; {
			pattern(frame, seq)
			if ring.Push(frame) {
				seq++
			}
		}
	}()

	want := make([]int16, 16)
	got := make([]int16, 16)
	for seq := 0; seq < frames; {
		if !ring.Pop(got) {
			continue
		}
		pattern(want, seq)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("frame %d sample %d = %d, want %d", seq, i, got[i], want[i])
			}
		}
		seq++
	}
	// Dropped counts rejected pushes (the producer above retries them), so
	// it is legitimately non-zero here; the per-sample comparison already
	// proves no frame was lost, duplicated or torn.
}
