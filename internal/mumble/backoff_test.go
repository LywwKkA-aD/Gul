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
		if got := backoffDelay(attempt, nil); got != expected {
			t.Errorf("backoffDelay(%d) = %s, want %s", attempt, got, expected)
		}
	}
}

func TestBackoffDelayIsClamped(t *testing.T) {
	if got := backoffDelay(-5, nil); got != backoffBase {
		t.Errorf("backoffDelay(-5) = %s, want %s", got, backoffBase)
	}
	// A long outage must not overflow the shift into a negative duration.
	for _, attempt := range []int{16, 64, 1_000, 1 << 30} {
		got := backoffDelay(attempt, nil)
		if got != backoffCap {
			t.Errorf("backoffDelay(%d) = %s, want %s", attempt, got, backoffCap)
		}
	}
}

func TestBackoffDelayAppliesJitterFromTheGivenSource(t *testing.T) {
	cases := []struct {
		name   string
		random float64
		want   time.Duration
	}{
		{name: "lower bound", random: 0, want: 800 * time.Millisecond},
		{name: "midpoint", random: 0.5, want: time.Second},
		{name: "upper bound", random: 0.999999, want: 1200 * time.Millisecond},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := backoffDelay(0, func() float64 { return tc.random })
			// The upper bound is open, so allow one millisecond of slack.
			if diff := got - tc.want; diff < -time.Millisecond || diff > time.Millisecond {
				t.Fatalf("backoffDelay(0, %v) = %s, want %s", tc.random, got, tc.want)
			}
		})
	}
}

// TestBackoffJitterSpreadsSynchronizedClients is the point of the jitter: every
// client knocked offline by the same server restart walks the same ladder, and
// without a spread they all come back in the same instant.
func TestBackoffJitterSpreadsSynchronizedClients(t *testing.T) {
	const clients = 64
	seen := map[time.Duration]int{}
	for client := range clients {
		// A deterministic stand-in for per-client randomness.
		random := float64(client) / float64(clients)
		seen[backoffDelay(3, func() float64 { return random })]++
	}

	if len(seen) < clients/2 {
		t.Fatalf("distinct delays = %d, want the clients spread out", len(seen))
	}
	base := baseBackoffDelay(3)
	for delay := range seen {
		if delay < time.Duration(float64(base)*(1-backoffJitter)) ||
			delay > time.Duration(float64(base)*(1+backoffJitter)) {
			t.Fatalf("delay %s is outside +/-%.0f%% of %s", delay, backoffJitter*100, base)
		}
	}
}

func TestDefaultBackoffStaysWithinTheJitteredLadder(t *testing.T) {
	for attempt := range 8 {
		base := baseBackoffDelay(attempt)
		low := time.Duration(float64(base) * (1 - backoffJitter))
		high := time.Duration(float64(base) * (1 + backoffJitter))
		for range 32 {
			if got := defaultBackoff(attempt); got < low || got > high {
				t.Fatalf("defaultBackoff(%d) = %s, want within [%s, %s]", attempt, got, low, high)
			}
		}
	}
}

// TestDefaultBackoffDrawsAFreshSpreadPerCall pins the wiring, not the maths:
// the ladder above is deterministic, so re-pointing the production function at
// the unjittered variant would keep every test but this one green - and put
// every client of a restarted server back in lockstep.
func TestDefaultBackoffDrawsAFreshSpreadPerCall(t *testing.T) {
	const calls = 32
	seen := map[time.Duration]bool{}
	for range calls {
		seen[defaultBackoff(3)] = true
	}

	// Nanosecond resolution over a 3.2s span: repeats mean a constant, not luck.
	if len(seen) < calls/2 {
		t.Fatalf("distinct delays = %d out of %d calls, want the attempt jittered per call",
			len(seen), calls)
	}
}
