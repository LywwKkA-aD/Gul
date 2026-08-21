package mumble

import (
	"testing"
	"time"
)

func TestBackoffDelaySequence(t *testing.T) {
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}

	for attempt, expected := range want {
		if got := backoffDelay(attempt); got != expected {
			t.Errorf("backoffDelay(%d) = %s, want %s", attempt, got, expected)
		}
	}
}

func TestBackoffDelayIsClamped(t *testing.T) {
	if got := backoffDelay(-5); got != backoffBase {
		t.Errorf("backoffDelay(-5) = %s, want %s", got, backoffBase)
	}
	// A long outage must not overflow the shift into a negative duration.
	for _, attempt := range []int{16, 64, 1_000, 1 << 30} {
		got := backoffDelay(attempt)
		if got != backoffCap {
			t.Errorf("backoffDelay(%d) = %s, want %s", attempt, got, backoffCap)
		}
	}
}
