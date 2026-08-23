package hotkey

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// source is the operating-system half of the poller. Splitting it out is what
// keeps the transition logic, the goroutine lifecycle and the Stop semantics
// testable with a fake on any platform, cgo or not.
//
// open, pressed and close all run on the poll goroutine, which is locked to an
// OS thread for the whole watch, so implementations need no locking of their
// own and may hold thread-affine handles. lookup is a pure table read and may
// be called from anywhere.
type source interface {
	// lookup maps a vocabulary name to this platform's key code.
	lookup(name string) (keyCode, bool)
	// open acquires whatever the platform needs to read key state.
	open() error
	// pressed reports whether code is physically down right now.
	pressed(code keyCode) (bool, error)
	// close releases what open acquired. It runs once, after the loop exits.
	close()
}

// tickerFunc creates the poll clock and a function that stops it. Production
// uses time.NewTicker; the tests drive the channel by hand so that transitions
// are exercised without waiting on real time.
type tickerFunc func(time.Duration) (<-chan time.Time, func())

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// releaseGrace is how long the key must keep reading as up before a release is
// reported. A press is reported on the very first read that sees the key down,
// because press latency is the half a talker hears as a clipped first word; a
// release is the half where a single glitched read would chop the tail off a
// sentence, so it has to be confirmed. The grace is expressed in time rather
// than in reads so that PollInterval does not change the behaviour.
const releaseGrace = 40 * time.Millisecond

// poller turns a source into a Monitor by reading one key on a ticker.
type poller struct {
	src        source
	interval   time.Duration
	capability Capability
	newTicker  tickerFunc

	mu     sync.Mutex
	active *pollWatch
}

// pollWatch is one Watch call's goroutine.
type pollWatch struct {
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newPoller(src source, interval time.Duration, capability Capability) *poller {
	return &poller{
		src:        src,
		interval:   interval,
		capability: capability,
		newTicker:  realTicker,
	}
}

func (p *poller) Capability() Capability { return p.capability }

func (p *poller) Watch(key string, onChange func(held bool)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// The previous watch ends before the new request is even validated: a
	// rejected Watch must leave the Monitor stopped (see the interface doc),
	// never silently keep an older binding alive.
	p.stopLocked()

	if onChange == nil {
		return fmt.Errorf("hotkey: Watch needs a callback")
	}
	if !validKey(key) {
		return fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	code, ok := p.src.lookup(key)
	if !ok {
		return fmt.Errorf("%w: %q on %s", ErrKeyUnavailable, key, runtime.GOOS)
	}

	w := &pollWatch{stop: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go p.run(w, code, onChange, ready)
	if err := <-ready; err != nil {
		<-w.done
		return err
	}
	p.active = w
	return nil
}

func (p *poller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

// stopLocked halts the current watch and waits for its goroutine. Waiting
// under p.mu is safe because the goroutine never takes p.mu.
func (p *poller) stopLocked() {
	w := p.active
	p.active = nil
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
}

// run owns one watch from open to close.
//
// The OS thread is pinned for the whole watch: the X11 source keeps a display
// connection that Xlib expects to be used from one thread at a time, and
// pinning also keeps the poll cadence off the scheduler's migration path.
// onChange runs here, on that thread, with no lock held.
func (p *poller) run(w *pollWatch, code keyCode, onChange func(held bool), ready chan<- error) {
	defer close(w.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := p.src.open(); err != nil {
		ready <- err
		return
	}
	defer p.src.close()
	ready <- nil

	tick, stopTicker := p.newTicker(p.interval)
	defer stopTicker()

	// A release needs confirming; one read is enough when the interval is
	// already longer than the grace.
	needUp := int(releaseGrace / p.interval)
	if needUp < 1 {
		needUp = 1
	}

	held := false
	upReads := 0
	for {
		select {
		case <-w.stop:
			// Deliberately silent: Stop promises no callback, and the caller
			// clears its own transmit state.
			return
		case <-tick:
			down, err := p.src.pressed(code)
			if err != nil {
				// The handle died under us. Reporting the release first is
				// the only safe order: a watch that ends while the key reads
				// as held would otherwise leave the microphone open with
				// nothing left to close it.
				if held {
					onChange(false)
				}
				return
			}
			switch {
			case down && !held:
				held = true
				upReads = 0
				onChange(true)
			case down:
				upReads = 0
			case held:
				upReads++
				if upReads >= needUp {
					held = false
					upReads = 0
					onChange(false)
				}
			}
		}
	}
}
