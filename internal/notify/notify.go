// Package notify decides when a system notification is warranted, and holds
// nothing else: no platform code, no strings, no I/O. The decision is a pure
// function of three things - what happened, what the window is doing, and how
// recently we last spoke - so it can be tested without a notification centre
// (PLAN.md 7 M4+).
//
// The policy in one sentence: notify about the channel we are in, only while
// the user is not looking at the window, and never often enough to fill the
// notification centre.
package notify

import (
	"sync"
	"time"
)

// Kind is what happened. Only some kinds are worth interrupting somebody for,
// and the set is deliberately small: a busy channel must not become a wall of
// notifications.
type Kind string

const (
	// KindMessage is a chat message in the channel we are in.
	KindMessage Kind = "message"
	// KindJoin is somebody arriving in the channel we are in.
	KindJoin Kind = "join"
	// KindLeave is somebody leaving it. It plays a cue and is NOT notified:
	// a departure is not something to come back to the window for.
	KindLeave Kind = "leave"
)

// notifiable is the whitelist. An unknown kind is refused rather than allowed,
// so adding an event elsewhere in the application cannot start notifying by
// accident.
func (k Kind) notifiable() bool {
	return k == KindMessage || k == KindJoin
}

// Window is what the application window is doing. Attended means the user is
// looking at it, and nothing is ever notified then - the whole point of a
// notification is to reach somebody who is elsewhere.
type Window struct {
	Visible bool
	Focused bool
}

// Attended reports whether the user is looking at the window.
//
// A hidden window cannot be focused, so in practice focus decides. Both are
// tracked because they arrive as separate facts from the platform, and the
// conservative combination is the one that stays silent when either says the
// user is present.
func (w Window) Attended() bool { return w.Visible && w.Focused }

// Rate limit. A token bucket: burst tokens are available at once, and one
// comes back every refill period. A conversation of three messages arriving
// together produces three notifications; a channel that is busy for an hour
// produces one every refill, not one per message.
const (
	// DefaultBurst is how many notifications may be sent back to back.
	DefaultBurst = 3
	// DefaultRefill is how long one spent token takes to come back.
	DefaultRefill = 20 * time.Second
)

// Decider applies the policy. Safe for concurrent use: the window state is
// written from the platform's window events and read from the goroutines that
// deliver chat and channel changes.
type Decider struct {
	mu     sync.Mutex
	window Window
	burst  int
	refill time.Duration
	tokens int
	last   time.Time
}

// New builds a decider with the given bucket. Non-positive values fall back to
// the defaults. The window starts ATTENDED: a platform that never reports
// focus must stay silent rather than notify over a window the user is looking
// at, which is the one thing this feature may not do.
func New(burst int, refill time.Duration) *Decider {
	if burst <= 0 {
		burst = DefaultBurst
	}
	if refill <= 0 {
		refill = DefaultRefill
	}
	return &Decider{
		window: Window{Visible: true, Focused: true},
		burst:  burst,
		refill: refill,
		tokens: burst,
	}
}

// SetWindow records what the window is doing now.
func (d *Decider) SetWindow(w Window) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = w
}

// Window returns the last state recorded.
func (d *Decider) Window() Window {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.window
}

// Should reports whether this event earns a notification, and spends a token
// when it says yes. at is the caller's clock, so the rate limit is testable
// without sleeping.
//
// Order matters: the window is checked before the bucket, so an event that
// happens while the user is watching costs nothing and leaves the whole burst
// available for the moment they walk away.
func (d *Decider) Should(kind Kind, at time.Time) bool {
	if !kind.notifiable() {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.window.Attended() {
		return false
	}
	if !d.take(at) {
		return false
	}
	return true
}

// take refills the bucket for the time that has passed and spends one token.
// Caller holds d.mu.
func (d *Decider) take(at time.Time) bool {
	if d.last.IsZero() {
		d.last = at
	}
	// A clock that went backwards must not hand out tokens or freeze the
	// bucket: it simply moves the reference forward without refilling.
	if elapsed := at.Sub(d.last); elapsed > 0 {
		if gained := int(elapsed / d.refill); gained > 0 {
			d.tokens = min(d.tokens+gained, d.burst)
			d.last = d.last.Add(time.Duration(gained) * d.refill)
		}
	} else if at.Before(d.last) {
		d.last = at
	}

	if d.tokens <= 0 {
		return false
	}
	d.tokens--
	// A bucket that has just been emptied starts refilling from now, not from
	// whenever the last refill was accounted for.
	if d.tokens == 0 {
		d.last = at
	}
	return true
}
