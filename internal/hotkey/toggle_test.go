package hotkey

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRegistrar imitates the slice of Wails' GlobalShortcutManager the Wayland
// backend uses, including its "error and preserve" behaviour for an
// accelerator that is already bound - that is what makes rebinding order
// observable.
type fakeRegistrar struct {
	mu          sync.Mutex
	bound       map[string]func()
	registers   []string
	unregisters []string
	registerErr error
}

func newFakeRegistrar() *fakeRegistrar {
	return &fakeRegistrar{bound: map[string]func(){}}
}

func (r *fakeRegistrar) Register(accelerator string, cb func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registerErr != nil {
		return r.registerErr
	}
	if _, exists := r.bound[accelerator]; exists {
		return fmt.Errorf("global shortcut %q is already registered", accelerator)
	}
	r.bound[accelerator] = cb
	r.registers = append(r.registers, accelerator)
	return nil
}

func (r *fakeRegistrar) Unregister(accelerator string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bound[accelerator]; !exists {
		return fmt.Errorf("global shortcut %q is not registered", accelerator)
	}
	delete(r.bound, accelerator)
	r.unregisters = append(r.unregisters, accelerator)
	return nil
}

// activate fires a bound shortcut the way Wails does, from a goroutine that is
// not the caller's.
func (r *fakeRegistrar) activate(t *testing.T, accelerator string) {
	t.Helper()
	r.mu.Lock()
	cb := r.bound[accelerator]
	r.mu.Unlock()
	if cb == nil {
		t.Fatalf("activate(%q): nothing bound", accelerator)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cb()
	}()
	<-done
}

func (r *fakeRegistrar) boundCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bound)
}

func (r *fakeRegistrar) log() (registers, unregisters []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.registers...), append([]string(nil), r.unregisters...)
}

type recorder struct {
	arrived chan bool

	mu     sync.Mutex
	events []bool
}

func newRecorder() *recorder {
	return &recorder{arrived: make(chan bool, 64)}
}

func (rec *recorder) record(held bool) {
	rec.mu.Lock()
	rec.events = append(rec.events, held)
	rec.mu.Unlock()
	select {
	case rec.arrived <- held:
	default:
	}
}

// waitFor blocks until n transitions have been delivered. Deliveries run on
// the latch's pump, so a test that asserted straight after an activation would
// be racing it.
func (rec *recorder) waitFor(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-rec.arrived:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d transitions were delivered", i, n)
		}
	}
}

func (rec *recorder) want(t *testing.T, want ...bool) {
	t.Helper()
	rec.mu.Lock()
	got := append([]bool(nil), rec.events...)
	rec.mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", got, want)
		}
	}
}

func TestToggleLatchesActivationsIntoHeldAndReleased(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	rec := newRecorder()
	if err := m.Watch("Space", rec.record); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer m.Stop()

	// The portal reports activations without releases, so the only honest
	// reading is a latch.
	reg.activate(t, "Space")
	reg.activate(t, "Space")
	reg.activate(t, "Space")
	rec.waitFor(t, 3)
	rec.want(t, true, false, true)
}

func TestToggleRegistersTheDerivedAccelerator(t *testing.T) {
	for _, tc := range []struct{ key, accel string }{
		{"Space", "Space"},
		{"KeyV", "V"},
		{"F13", "F13"},
		{"PageDown", "Page Down"},
		{"ArrowUp", "Up"},
		{"Backquote", "`"},
	} {
		reg := newFakeRegistrar()
		m := newToggleMonitor(reg, waylandToggleReason)
		if err := m.Watch(tc.key, func(bool) {}); err != nil {
			t.Fatalf("Watch(%q): %v", tc.key, err)
		}
		registers, _ := reg.log()
		if len(registers) != 1 || registers[0] != tc.accel {
			t.Fatalf("Watch(%q) registered %v, want [%q]", tc.key, registers, tc.accel)
		}
		m.Stop()
	}
}

func TestToggleRebindingReleasesThePreviousBindingFirst(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	defer m.Stop()

	if err := m.Watch("Space", func(bool) {}); err != nil {
		t.Fatalf("first Watch: %v", err)
	}
	// Rebinding the same key is the sharp case: a registrar refuses an
	// accelerator it already holds, so this only succeeds if the old binding
	// went away first.
	if err := m.Watch("Space", func(bool) {}); err != nil {
		t.Fatalf("rebinding the same key: %v", err)
	}
	registers, unregisters := reg.log()
	if len(registers) != 2 || len(unregisters) != 1 {
		t.Fatalf("registers = %v, unregisters = %v, want two and one", registers, unregisters)
	}
	if reg.boundCount() != 1 {
		t.Fatalf("bound accelerators = %d, want 1", reg.boundCount())
	}
}

func TestToggleRebindingStopsTheOldLatch(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	defer m.Stop()

	stale := newRecorder()
	if err := m.Watch("Space", stale.record); err != nil {
		t.Fatalf("first Watch: %v", err)
	}
	// Keep a handle on the first callback: a registrar can be slow to drop a
	// binding, and a late activation must not reach the replaced watch.
	reg.mu.Lock()
	oldCallback := reg.bound["Space"]
	reg.mu.Unlock()

	fresh := newRecorder()
	if err := m.Watch("KeyV", fresh.record); err != nil {
		t.Fatalf("second Watch: %v", err)
	}
	oldCallback()
	stale.want(t)

	reg.activate(t, "V")
	fresh.waitFor(t, 1)
	fresh.want(t, true)
}

func TestToggleStopUnbindsAndSilencesLateActivations(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	rec := newRecorder()
	if err := m.Watch("Space", rec.record); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	reg.mu.Lock()
	callback := reg.bound["Space"]
	reg.mu.Unlock()

	reg.activate(t, "Space")
	rec.waitFor(t, 1)
	m.Stop()

	if reg.boundCount() != 0 {
		t.Fatalf("bound accelerators after Stop = %d, want 0", reg.boundCount())
	}
	// An activation that was already on its way must not reopen the gate.
	callback()
	rec.want(t, true)
}

func TestToggleStopIsIdempotent(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	if err := m.Watch("Space", func(bool) {}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	m.Stop()
	m.Stop()
	m.Stop()
	_, unregisters := reg.log()
	if len(unregisters) != 1 {
		t.Fatalf("unregisters = %v, want exactly one", unregisters)
	}
}

func TestToggleStopWithoutWatchIsHarmless(t *testing.T) {
	reg := newFakeRegistrar()
	newToggleMonitor(reg, waylandToggleReason).Stop()
	registers, unregisters := reg.log()
	if len(registers) != 0 || len(unregisters) != 0 {
		t.Fatalf("an unused monitor touched the registrar: %v / %v", registers, unregisters)
	}
}

func TestToggleStopWaitsForAnActivationInFlight(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)

	entered := make(chan struct{})
	release := make(chan struct{})
	if err := m.Watch("Space", func(bool) {
		close(entered)
		<-release
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	reg.mu.Lock()
	callback := reg.bound["Space"]
	reg.mu.Unlock()

	go callback()
	<-entered

	stopped := make(chan struct{})
	go func() {
		m.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while an activation was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-stopped
}

func TestToggleRejectsKeysWithNoShortcutForm(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	// Modifiers, the keypad, CapsLock and Insert have no accelerator; binding
	// them to an approximation would hand the user a key they did not pick.
	for _, key := range []string{
		"ControlLeft", "ShiftRight", "AltLeft", "MetaRight",
		"CapsLock", "Insert",
		"Numpad5", "NumpadAdd", "NumpadDivide",
	} {
		err := m.Watch(key, func(bool) {})
		if !errors.Is(err, ErrKeyUnavailable) {
			t.Fatalf("Watch(%q) = %v, want ErrKeyUnavailable", key, err)
		}
	}
	if registers, _ := reg.log(); len(registers) != 0 {
		t.Fatalf("a rejected key still registered %v", registers)
	}
}

func TestToggleRejectsUnknownKeys(t *testing.T) {
	m := newToggleMonitor(newFakeRegistrar(), waylandToggleReason)
	if err := m.Watch("Spacebar", func(bool) {}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Watch = %v, want ErrUnknownKey", err)
	}
}

func TestToggleWatchNeedsACallback(t *testing.T) {
	m := newToggleMonitor(newFakeRegistrar(), waylandToggleReason)
	if err := m.Watch("Space", nil); err == nil {
		t.Fatal("Watch accepted a nil callback")
	}
}

func TestToggleReportsAFailedRegistration(t *testing.T) {
	reg := newFakeRegistrar()
	reg.registerErr = errors.New("compositor said no")
	m := newToggleMonitor(reg, waylandToggleReason)

	err := m.Watch("Space", func(bool) {})
	if !errors.Is(err, reg.registerErr) {
		t.Fatalf("Watch = %v, want the registrar's error", err)
	}
	// A failed Watch leaves nothing bound and nothing to unbind.
	m.Stop()
	if _, unregisters := reg.log(); len(unregisters) != 0 {
		t.Fatalf("Stop after a failed Watch unregistered %v", unregisters)
	}
}

func TestToggleWithoutARegistrarIsUnsupported(t *testing.T) {
	m := newToggleMonitor(nil, waylandToggleReason)
	capability := m.Capability()
	if capability.Mode != ModeUnsupported {
		t.Fatalf("Mode = %v, want ModeUnsupported", capability.Mode)
	}
	if capability.Reason != waylandNoPortalReason {
		t.Fatalf("Reason = %q, want the no-portal reason", capability.Reason)
	}
	if err := m.Watch("Space", func(bool) {}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Watch = %v, want ErrUnsupported", err)
	}
}

func TestToggleCapabilityCarriesTheRussianReason(t *testing.T) {
	m := newToggleMonitor(newFakeRegistrar(), waylandToggleReason)
	capability := m.Capability()
	if capability.Mode != ModeToggle {
		t.Fatalf("Mode = %v, want ModeToggle", capability.Mode)
	}
	// The reason is the only user-facing text this package produces, and the
	// user interface renders it as is.
	if !strings.Contains(capability.Reason, "режим переключения") {
		t.Fatalf("Reason = %q, want it to name the toggle mode", capability.Reason)
	}
	if !strings.Contains(capability.Reason, "композитор") {
		t.Fatalf("Reason = %q, want it to warn that the compositor picks the combination", capability.Reason)
	}
}

func TestToggleSurvivesConcurrentActivationsAndStop(t *testing.T) {
	reg := newFakeRegistrar()
	m := newToggleMonitor(reg, waylandToggleReason)
	rec := newRecorder()
	if err := m.Watch("Space", rec.record); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	reg.mu.Lock()
	callback := reg.bound["Space"]
	reg.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callback()
		}()
	}
	m.Stop()
	wg.Wait()

	// Deliveries follow the latch, so however the activations interleave the
	// caller can never be told the same state twice in a row. Before the
	// deliveries were serialised, this is the invariant that broke.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for i := 1; i < len(rec.events); i++ {
		if rec.events[i] == rec.events[i-1] {
			t.Fatalf("latch repeated a state: %v", rec.events)
		}
	}
}
