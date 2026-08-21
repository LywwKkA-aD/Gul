package mumble

import "time"

const (
	// backoffBase is the delay before the first reconnect attempt.
	backoffBase = time.Second
	// backoffCap bounds the delay so a long outage still reconnects promptly
	// once the server returns.
	backoffCap = 30 * time.Second
	// backoffMaxShift guards the shift below against overflow; 2^5 s already
	// exceeds the cap, so anything beyond is clamped anyway.
	backoffMaxShift = 16
)

// backoffDelay returns how long to wait before reconnect attempt n, counting
// from zero: 1s, 2s, 4s, 8s, 16s, then 30s forever.
//
// Kept pure and free of any Manager state so the sequence is directly testable.
func backoffDelay(attempt int) time.Duration {
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
