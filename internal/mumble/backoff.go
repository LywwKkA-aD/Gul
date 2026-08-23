package mumble

import (
	"math/rand/v2"
	"time"
)

const (
	// backoffBase is the delay before the first reconnect attempt.
	backoffBase = time.Second
	// backoffCap bounds the delay so a long outage still reconnects promptly
	// once the server returns.
	backoffCap = 30 * time.Second
	// backoffMaxShift guards the shift below against overflow; 2^5 s already
	// exceeds the cap, so anything beyond is clamped anyway.
	backoffMaxShift = 16
	// backoffJitter spreads the delay by +/-20%. Every client of a server that
	// restarts is knocked offline in the same second and would otherwise walk
	// the identical 1s, 2s, 4s ... ladder in lockstep, hitting the returning
	// server as one synchronized wave.
	backoffJitter = 0.2
)

// backoffDelay returns how long to wait before reconnect attempt n, counting
// from zero: 1s, 2s, 4s, 8s, 16s, then 30s forever, each spread by +/-20%.
//
// random returns a value in [0, 1); nil means an unjittered delay, which is
// what tests use to pin the ladder itself.
//
// Kept pure and free of any Manager state so the sequence is directly testable.
func backoffDelay(attempt int, random func() float64) time.Duration {
	return jitter(baseBackoffDelay(attempt), random)
}

func baseBackoffDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return backoffBase
	}
	if attempt >= backoffMaxShift {
		return backoffCap
	}
	d := backoffBase << uint(attempt)
	if d > backoffCap {
		return backoffCap
	}
	return d
}

// jitter scales d by a factor in [1-backoffJitter, 1+backoffJitter).
func jitter(d time.Duration, random func() float64) time.Duration {
	if random == nil {
		return d
	}
	factor := 1 - backoffJitter + 2*backoffJitter*random()
	jittered := time.Duration(float64(d) * factor)
	if jittered <= 0 {
		return d
	}
	return jittered
}

// defaultBackoff is the production sequence. rand.Float64 is safe for
// concurrent use, so one Manager per process needs no source of its own.
func defaultBackoff(attempt int) time.Duration {
	return backoffDelay(attempt, rand.Float64)
}
