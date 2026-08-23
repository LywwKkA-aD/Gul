package hotkey

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSource stands in for the operating system so the poller's transitions,
// lifecycle and Stop semantics are exercised on every platform, cgo or not.
//
// polled is what makes the tests deterministic: it is signalled once the read
// for a tick has taken its snapshot, so the test knows when it may stage the
// next key state without racing the poll it just triggered.
type fakeSource struct {
	polled chan struct{}

	mu       sync.Mutex
	down     bool
	openErr  error
	pressErr error
	opens    int
	closes   int
	polls    int
}

func (f *fakeSource) lookup(name string) (keyCode, bool) {
	// Everything in the vocabulary resolves except one name held back for the
	// "valid, but not addressable here" case.
	if !validKey(name) || name == "F24" {
		return 0, false
	}
	return 42, true
}

func (f *fakeSource) open() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	return f.openErr
}

func (f *fakeSource) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
}

func (f *fakeSource) pressed(keyCode) (bool, error) {
	f.mu.Lock()
	f.polls++
	down, err := f.down, f.pressErr
	f.mu.Unlock()
	select {
	case f.polled <- struct{}{}:
	default: // a test that does not wait for reads must not stall the poller
	}
	return down, err
}

func (f *fakeSource) set(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeSource) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pressErr = err
}

func (f *fakeSource) counts() (opens, closes, polls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens, f.closes, f.polls
}

// harness drives a poller by hand.
//
// Transitions are asserted only after Stop, which waits for the poll
// goroutine: at that point every callback the run could produce has already
// been produced, so the recorded sequence is complete rather than merely
// current.
type harness struct {
	t   *testing.T
	p   *poller
	src *fakeSource

	tick        chan time.Time
	tickerStops int

	mu     sync.Mutex
	events []bool
}

func newHarness(t *testing.T, interval time.Duration) *harness {
	t.Helper()
	h := &harness{
		t:    t,
		src:  &fakeSource{polled: make(chan struct{}, 256)},
		tick: make(chan time.Time),
	}
	h.p = newPoller(h.src, interval, Capability{Mode: ModeHold})
	h.p.newTicker = func(time.Duration) (<-chan time.Time, func()) {
		return h.tick, func() { h.tickerStops++ }
	}
	t.Cleanup(h.p.Stop)
	return h
}

func (h *harness) record(held bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, held)
}

func (h *harness) watch(key string) error {
	return h.p.Watch(key, h.record)
}

// poll stages a key state and drives exactly one read of it.
func (h *harness) poll(down bool) {
	h.t.Helper()
	h.src.set(down)
	h.tick <- time.Now()
	<-h.src.polled
}

func (h *harness) wantEvents(want ...bool) {
	h.t.Helper()
	h.mu.Lock()
	got := append([]bool(nil), h.events...)
	h.mu.Unlock()
	if len(got) != len(want) {
		h.t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			h.t.Fatalf("transitions = %v, want %v", got, want)
		}
	}
}

func TestPollerReportsAPressOnTheFirstRead(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)
	h.p.Stop()
	// One read of a held key is all a press may cost: waiting for a second
	// would clip the start of the first word.
	h.wantEvents(true)
}

func TestPollerHoldsAReleaseUntilItIsConfirmed(t *testing.T) {
	h := newHarness(t, DefaultPollInterval) // 40 ms grace over 20 ms reads
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)
	h.poll(false)
	h.p.Stop()
	h.wantEvents(true)
}

func TestPollerReportsAReleaseOnceTheGraceHasPassed(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)
	h.poll(false)
	h.poll(false)
	h.p.Stop()
	h.wantEvents(true, false)
}

func TestPollerSwallowsASingleGlitchedRead(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("KeyV"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)
	// One read up, then held again: the confirmation resets, so a talker
	// keeps the transmission through a hiccup in the key state.
	h.poll(false)
	h.poll(true)
	h.poll(true)
	h.p.Stop()
	h.wantEvents(true)
}

func TestPollerConfirmsAReleaseInOneReadWhenTheIntervalIsLong(t *testing.T) {
	// A read every 100 ms already outlasts the grace, so a release must not
	// be made to wait for a second one.
	h := newHarness(t, 100*time.Millisecond)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)
	h.poll(false)
	h.p.Stop()
	h.wantEvents(true, false)
}

func TestPollerReportsRepeatedPresses(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	for i := 0; i < 3; i++ {
		h.poll(true)
		h.poll(false)
		h.poll(false)
	}
	h.p.Stop()
	h.wantEvents(true, false, true, false, true, false)
}

func TestPollerStopDeliversNoReleaseForAHeldKey(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("ControlRight"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)

	h.p.Stop()
	// Stop waits for the goroutine, so nothing can arrive after it returns.
	h.wantEvents(true)

	if _, closes, _ := h.src.counts(); closes != 1 {
		t.Fatalf("source closes = %d, want 1", closes)
	}
	if h.tickerStops != 1 {
		t.Fatalf("ticker stops = %d, want 1", h.tickerStops)
	}
}

func TestPollerStopIsIdempotent(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.p.Stop()
	h.p.Stop()
	h.p.Stop()
	if opens, closes, _ := h.src.counts(); opens != 1 || closes != 1 {
		t.Fatalf("opens/closes = %d/%d, want 1/1", opens, closes)
	}
}

func TestPollerStopBeforeAnyWatchIsHarmless(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	h.p.Stop()
	if opens, closes, polls := h.src.counts(); opens != 0 || closes != 0 || polls != 0 {
		t.Fatalf("untouched source got opens/closes/polls = %d/%d/%d", opens, closes, polls)
	}
}

func TestPollerWatchReplacesThePreviousWatch(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("first Watch: %v", err)
	}
	if err := h.watch("KeyQ"); err != nil {
		t.Fatalf("second Watch: %v", err)
	}
	if opens, closes, _ := h.src.counts(); opens != 2 || closes != 1 {
		t.Fatalf("opens/closes = %d/%d, want 2/1: the first watch must be torn down", opens, closes)
	}

	h.poll(true)
	h.p.Stop()
	h.wantEvents(true)
	if opens, closes, _ := h.src.counts(); opens != 2 || closes != 2 {
		t.Fatalf("opens/closes after Stop = %d/%d, want 2/2", opens, closes)
	}
}

// TestPollerRejectedWatchLeavesTheMonitorStopped pins the interface promise:
// a Watch that fails validation tears down the previous watch instead of
// silently keeping an older binding alive.
func TestPollerRejectedWatchLeavesTheMonitorStopped(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("first Watch: %v", err)
	}
	if err := h.watch("Spacebar"); err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if opens, closes, _ := h.src.counts(); opens != 1 || closes != 1 {
		t.Fatalf("opens/closes = %d/%d, want 1/1: the first watch must be torn down", opens, closes)
	}
	if h.p.active != nil {
		t.Fatal("a rejected Watch left a watch active")
	}
}

func TestPollerWatchAfterStopStartsAgain(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.p.Stop()
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch after Stop: %v", err)
	}
	h.poll(true)
	h.p.Stop()
	h.wantEvents(true)
}

func TestPollerWatchRejectsUnknownKeys(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	for _, name := range []string{"", "Spacebar", "space", "KeyÄ", "F25", "Numpad10", "Meta"} {
		err := h.watch(name)
		if !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("Watch(%q) = %v, want ErrUnknownKey", name, err)
		}
	}
	if opens, _, _ := h.src.counts(); opens != 0 {
		t.Fatalf("a rejected key still opened the source %d times", opens)
	}
}

func TestPollerWatchRejectsKeysThePlatformCannotAddress(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	err := h.watch("F24") // a valid name, absent from the fake's table
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Watch = %v, want ErrKeyUnavailable", err)
	}
	if errors.Is(err, ErrUnknownKey) {
		t.Fatal("a platform limit must not be reported as a misspelled key")
	}
}

func TestPollerWatchNeedsACallback(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.p.Watch("Space", nil); err == nil {
		t.Fatal("Watch accepted a nil callback")
	}
}

func TestPollerWatchReportsAFailedOpen(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	sentinel := errors.New("no display")
	h.src.openErr = sentinel

	err := h.watch("Space")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Watch = %v, want %v", err, sentinel)
	}
	if opens, closes, _ := h.src.counts(); opens != 1 || closes != 0 {
		t.Fatalf("opens/closes = %d/%d, want 1/0: nothing was acquired to release", opens, closes)
	}
	// A failed Watch leaves the monitor stopped, not half started.
	h.p.Stop()
	h.wantEvents()
}

func TestPollerReleasesTheKeyWhenTheSourceDies(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.poll(true)

	// A watch that dies while the key reads as held must close the gate on
	// its way out; nothing else is left to do it.
	h.src.fail(errors.New("display gone"))
	h.poll(true)

	h.p.Stop()
	h.wantEvents(true, false)
	if _, closes, _ := h.src.counts(); closes != 1 {
		t.Fatalf("source closes = %d, want 1", closes)
	}
}

func TestPollerDyingSourceIsSilentWhenTheKeyWasUp(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.src.fail(errors.New("display gone"))
	h.poll(false)

	h.p.Stop()
	h.wantEvents()
}

func TestPollerStopWaitsForACallbackInFlight(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})

	err := h.p.Watch("Space", func(bool) {
		close(entered)
		<-release
		close(finished)
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	h.src.set(true)
	go func() { h.tick <- time.Now() }()
	<-entered

	stopped := make(chan struct{})
	go func() {
		h.p.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned while a callback was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-finished
	<-stopped
}

func TestPollerPassesTheIntervalToTheTicker(t *testing.T) {
	// The interval reaches the ticker unchanged: a poller that ignored it
	// could only be reading as fast as the scheduler allows.
	h := newHarness(t, 33*time.Millisecond)
	var seen time.Duration
	h.p.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		seen = d
		return h.tick, func() { h.tickerStops++ }
	}
	if err := h.watch("Space"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	h.p.Stop()
	if seen != 33*time.Millisecond {
		t.Fatalf("ticker interval = %s, want 33ms", seen)
	}
}

func TestPollerCapabilityIsFixed(t *testing.T) {
	h := newHarness(t, DefaultPollInterval)
	if got := h.p.Capability(); got.Mode != ModeHold || got.Reason != "" {
		t.Fatalf("Capability = %+v, want hold with no reason", got)
	}
}
