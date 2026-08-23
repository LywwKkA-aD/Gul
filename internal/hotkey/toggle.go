package hotkey

import (
	"fmt"
	"sync"
)

// Toggle-to-talk, the Wayland fallback.
//
// A compositor never lets a client read key state, and the portal reports an
// activation with no matching release, so holding the key cannot be observed
// at all. The only honest behaviour left is a latch: one activation opens the
// microphone, the next closes it. The mode is reported through Capability so
// the user interface can say so instead of leaving the user to discover it
// mid-sentence.

// waylandToggleReason is what the user is told when the session forces a
// latch. The second sentence matters as much as the first: the portal hands
// the compositor a *preferred* trigger and the compositor may bind something
// else entirely, so the settings screen has to show what was really bound.
const waylandToggleReason = "На Wayland доступен только режим переключения: " +
	"нажатие включает передачу, повторное — выключает. " +
	"Итоговое сочетание назначает композитор, поэтому оно может отличаться от выбранного."

// waylandNoPortalReason covers a Wayland session with no registrar wired in.
const waylandNoPortalReason = "На Wayland глобальная клавиша недоступна: " +
	"нет доступа к порталу горячих клавиш."

// toggleMonitor latches a Registrar activation into held/released transitions.
type toggleMonitor struct {
	reg        Registrar
	capability Capability

	mu    sync.Mutex
	accel string
	watch *toggleWatch
}

func newToggleMonitor(reg Registrar, reason string) Monitor {
	if reg == nil {
		return newUnsupported(waylandNoPortalReason)
	}
	return &toggleMonitor{
		reg:        reg,
		capability: Capability{Mode: ModeToggle, Reason: reason},
	}
}

func (m *toggleMonitor) Capability() Capability { return m.capability }

func (m *toggleMonitor) Watch(key string, onChange func(held bool)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// The previous binding goes first, before validation: a registrar
	// refuses to bind an accelerator it already holds, and a rejected Watch
	// must leave the Monitor stopped (see the interface doc).
	m.stopLocked()

	if onChange == nil {
		return fmt.Errorf("hotkey: Watch needs a callback")
	}
	if !validKey(key) {
		return fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	accel, ok := acceleratorFor(key)
	if !ok {
		return fmt.Errorf("%w: %q has no global shortcut form", ErrKeyUnavailable, key)
	}

	w := newToggleWatch(onChange)
	if err := m.reg.Register(accel, w.fire); err != nil {
		// The pump is already running; a rejected binding must not leave it
		// behind.
		w.stop()
		return fmt.Errorf("hotkey: register %q for %q: %w", accel, key, err)
	}
	m.accel, m.watch = accel, w
	return nil
}

func (m *toggleMonitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *toggleMonitor) stopLocked() {
	w, accel := m.watch, m.accel
	m.watch, m.accel = nil, ""
	if w == nil {
		return
	}
	// Close the callback path before dropping the binding: once stop returns,
	// an activation still in flight can no longer reach the caller.
	w.stop()
	// A registrar that has already forgotten the accelerator leaves nothing
	// to undo, and Stop has no way to report it either.
	_ = m.reg.Unregister(accel)
}

// toggleWatch is one Watch call's latch.
//
// Activations arrive on whatever goroutine the registrar dispatches from -
// Wails uses a fresh one per activation - so two of them can flip the latch in
// one order and then reach the caller in the other, which would leave the
// microphone in the opposite state to the latch. Deliveries therefore go
// through a queue drained by a single goroutine: the flip happens under the
// lock, the delivery happens on the pump with no lock held, and the order the
// caller sees is the order the latch produced.
//
// fire never runs the callback itself, so a slow caller cannot block the
// registrar's dispatch either.
type toggleWatch struct {
	fn func(held bool)

	mu      sync.Mutex
	ready   *sync.Cond
	done    bool
	held    bool
	queue   []bool
	drained chan struct{}
}

func newToggleWatch(fn func(held bool)) *toggleWatch {
	w := &toggleWatch{fn: fn, drained: make(chan struct{})}
	w.ready = sync.NewCond(&w.mu)
	go w.pump()
	return w
}

func (w *toggleWatch) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.held = !w.held
	w.queue = append(w.queue, w.held)
	w.ready.Signal()
}

func (w *toggleWatch) pump() {
	defer close(w.drained)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.done {
			w.ready.Wait()
		}
		if w.done {
			w.mu.Unlock()
			return
		}
		batch := w.queue
		w.queue = nil
		w.mu.Unlock()

		for _, held := range batch {
			w.fn(held)
		}
	}
}

// stop closes the latch and waits for the pump, so once it returns no further
// callback can arrive. Whatever is still queued is dropped rather than
// delivered: Stop means the caller has torn its push-to-talk down and owns
// clearing its own transmit state, exactly as with the polling backend.
//
// It is idempotent.
func (w *toggleWatch) stop() {
	w.mu.Lock()
	w.done = true
	w.queue = nil
	w.ready.Broadcast()
	w.mu.Unlock()
	<-w.drained
}
