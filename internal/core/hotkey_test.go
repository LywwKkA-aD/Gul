package core

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/hotkey"
)

// ---------------------------------------------------------------------------
// doubles
// ---------------------------------------------------------------------------

// fakeMonitor stands in for internal/hotkey. It records what was watched and
// hands the test the callback, so a key transition is a method call rather
// than a real key press.
type fakeMonitor struct {
	capability hotkey.Capability

	// callMu models the delivery contract of the real Monitor: Stop waits
	// for a callback already in flight. Core must therefore never hold its
	// own lock across Stop, and this is what would deadlock if it did.
	callMu sync.Mutex

	mu       sync.Mutex
	watchErr error
	watched  []string
	stops    int
	onChange func(bool)
}

func (m *fakeMonitor) Watch(key string, onChange func(held bool)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watchErr != nil {
		m.onChange = nil
		return m.watchErr
	}
	m.watched = append(m.watched, key)
	m.onChange = onChange
	return nil
}

func (m *fakeMonitor) Stop() {
	m.callMu.Lock()
	defer m.callMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.stops++
	// The real Monitor delivers nothing after Stop, not even a release.
	m.onChange = nil
}

func (m *fakeMonitor) Capability() hotkey.Capability { return m.capability }

// fire delivers a transition the way the polling goroutine would, holding the
// delivery lock for as long as the callback runs.
func (m *fakeMonitor) fire(held bool) {
	m.callMu.Lock()
	defer m.callMu.Unlock()

	m.mu.Lock()
	fn := m.onChange
	m.mu.Unlock()
	if fn != nil {
		fn(held)
	}
}

func (m *fakeMonitor) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.watched...)
}

func (m *fakeMonitor) stopCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stops
}

func (m *fakeMonitor) isWatching() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onChange != nil
}

var _ hotkey.Monitor = (*fakeMonitor)(nil)

// panicVoice fails the way a broken engine adapter would: inside the callback,
// on the goroutine internal/hotkey polls from.
type panicVoice struct{ fakeVoice }

func (v *panicVoice) SetPTT(bool) { panic("engine gone") }

// newHotkeyApp builds a core with the given gate settings, a recording engine
// and a fake monitor. Nothing is persisted (no directory) and the debounce
// window is driven by hand, so no timer outlives the test.
func newHotkeyApp(t *testing.T, gate config.Gate, capability hotkey.Capability) (*App, *fakeVoice, *fakeMonitor) {
	t.Helper()
	app := New(discardLogger(), nil)
	app.saver.after = newFakeClock().after
	voice := &fakeVoice{}
	app.SetVoice(voice)

	cfg := config.Defaults()
	cfg.Gate = gate
	app.UseSettings("", cfg, nil)

	monitor := &fakeMonitor{capability: capability}
	app.SetKeyMonitor(monitor)
	return app, voice, monitor
}

func gateWith(mode config.GateMode, global bool) config.Gate {
	gate := config.Gate{Mode: mode, OpenThreshold: 0.6, HangoverMs: 300, PTTKey: "KeyV"}
	gate.GlobalPTT = global
	return gate
}

// ---------------------------------------------------------------------------
// the watch rule
// ---------------------------------------------------------------------------

// The key is watched system wide only when the gate is in push-to-talk, the
// user asked for it, and the machine can watch keys at all. Everything else
// leaves the window-focused listener as the only source.
func TestGlobalPTTWatchRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode      config.GateMode
		global    bool
		mode2     hotkey.Mode
		wantWatch bool
	}{
		{config.GateModePTT, true, hotkey.ModeHold, true},
		{config.GateModePTT, true, hotkey.ModeToggle, true},
		{config.GateModePTT, true, hotkey.ModeUnsupported, false},
		{config.GateModePTT, false, hotkey.ModeHold, false},
		{config.GateModePTT, false, hotkey.ModeToggle, false},
		{config.GateModePTT, false, hotkey.ModeUnsupported, false},
		{config.GateModeVAD, true, hotkey.ModeHold, false},
		{config.GateModeVAD, true, hotkey.ModeToggle, false},
		{config.GateModeVAD, true, hotkey.ModeUnsupported, false},
		{config.GateModeVAD, false, hotkey.ModeHold, false},
		{config.GateModeVAD, false, hotkey.ModeToggle, false},
		{config.GateModeVAD, false, hotkey.ModeUnsupported, false},
	}
	for _, c := range cases {
		name := fmt.Sprintf("%s/global=%v/%s", c.mode, c.global, c.mode2)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, monitor := newHotkeyApp(t, gateWith(c.mode, c.global),
				hotkey.Capability{Mode: c.mode2})

			if got := monitor.isWatching(); got != c.wantWatch {
				t.Fatalf("watching = %v, want %v", got, c.wantWatch)
			}
			if c.wantWatch {
				if keys := monitor.keys(); len(keys) != 1 || keys[0] != "KeyV" {
					t.Fatalf("watched keys = %v, want [KeyV]", keys)
				}
			} else if keys := monitor.keys(); len(keys) != 0 {
				t.Fatalf("watched keys = %v, want none", keys)
			}
		})
	}
}

// Every mutator that can change the answer re-decides, and the transitions in
// both directions have to work on a live core, not just at startup.
func TestGlobalPTTFollowsTheSettings(t *testing.T) {
	t.Parallel()
	app, _, monitor := newHotkeyApp(t, gateWith(config.GateModeVAD, false),
		hotkey.Capability{Mode: hotkey.ModeHold})

	app.SetGlobalPTT(true)
	if monitor.isWatching() {
		t.Fatal("watching in voice-activation mode")
	}
	if err := app.SetGateMode(string(config.GateModePTT)); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	if !monitor.isWatching() {
		t.Fatal("not watching with push-to-talk and the global key on")
	}

	// A key change re-points the watch instead of leaving the old key live.
	if err := app.SetPTTKey("KeyB"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	if keys := monitor.keys(); len(keys) != 2 || keys[1] != "KeyB" {
		t.Fatalf("watched keys = %v, want the second key", keys)
	}
	// Setting the same key again is not a reason to restart the watch.
	if err := app.SetPTTKey("KeyB"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	if keys := monitor.keys(); len(keys) != 2 {
		t.Fatalf("watched keys = %v, want no extra watch", keys)
	}

	app.SetGlobalPTT(false)
	if monitor.isWatching() {
		t.Fatal("still watching after the global key was turned off")
	}
}

// ---------------------------------------------------------------------------
// releasing on every stop path
// ---------------------------------------------------------------------------

// Stop delivers nothing, so whoever ends the watch owns closing the
// transmission the key may have opened.
func TestGlobalPTTReleasesOnEveryStopPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		stop func(app *App)
	}{
		{"global off", func(app *App) { app.SetGlobalPTT(false) }},
		{"mode change", func(app *App) {
			if err := app.SetGateMode(string(config.GateModeVAD)); err != nil {
				t.Errorf("SetGateMode: %v", err)
			}
		}},
		{"key change", func(app *App) {
			if err := app.SetPTTKey("KeyB"); err != nil {
				t.Errorf("SetPTTKey: %v", err)
			}
		}},
		{"shutdown", func(app *App) { app.StopGlobalPTT() }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app, voice, monitor := newHotkeyApp(t, gateWith(config.GateModePTT, true),
				hotkey.Capability{Mode: hotkey.ModeHold})

			monitor.fire(true)
			if got := voice.snapshot().ptt; len(got) != 1 || !got[0] {
				t.Fatalf("ptt = %v, want [true] while the key is held", got)
			}

			c.stop(app)

			if monitor.stopCount() == 0 {
				t.Error("the monitor was never stopped")
			}
			got := voice.snapshot().ptt
			if len(got) < 2 || got[len(got)-1] {
				t.Fatalf("ptt = %v, want a release at the end", got)
			}
		})
	}
}

// A watch that never started must not release: the window-focused listener may
// be holding the key right now, and cutting it would end the sentence.
func TestGlobalPTTDoesNotReleaseWithoutAWatch(t *testing.T) {
	t.Parallel()
	app, voice, _ := newHotkeyApp(t, gateWith(config.GateModeVAD, false),
		hotkey.Capability{Mode: hotkey.ModeHold})

	app.SetPTT(true)
	app.SetGlobalPTT(true)
	app.SetGlobalPTT(false)
	app.StopGlobalPTT()

	if got := voice.snapshot().ptt; len(got) != 1 || !got[0] {
		t.Fatalf("ptt = %v, want only what the caller asked for", got)
	}
}

// ---------------------------------------------------------------------------
// errors
// ---------------------------------------------------------------------------

// The key vocabulary of internal/hotkey is narrower than the one the settings
// document accepts, so a stored key can be refused. The user is told, and the
// setting keeps what was asked for rather than being reverted behind the UI.
func TestGlobalPTTReportsAnUnknownKey(t *testing.T) {
	t.Parallel()
	app, voice, monitor := newHotkeyApp(t, gateWith(config.GateModeVAD, false),
		hotkey.Capability{Mode: hotkey.ModeHold})
	monitor.mu.Lock()
	monitor.watchErr = fmt.Errorf("%w: %q", hotkey.ErrUnknownKey, "KeyV")
	monitor.mu.Unlock()

	if err := app.SetGateMode(string(config.GateModePTT)); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	app.SetGlobalPTT(true)

	status := app.HotkeyStatus()
	if !strings.Contains(status.Error, "KeyV") || !strings.Contains(status.Error, "недоступна") {
		t.Fatalf("HotkeyStatus().Error = %q, want the Russian message naming the key", status.Error)
	}
	// What the user chose survives: only the watch failed.
	cfg := app.Settings()
	if !cfg.Gate.GlobalPTT || cfg.Gate.PTTKey != "KeyV" || cfg.Gate.Mode != config.GateModePTT {
		t.Fatalf("settings = %+v, want the request stored as made", cfg.Gate)
	}
	// A Watch that never bound must not touch the gate: the window listener
	// legitimately owns push-to-talk while the global key is unavailable, and
	// closing here would cut off a phrase the user is in the middle of.
	if got := voice.snapshot().ptt; len(got) != 0 {
		t.Fatalf("ptt = %v, want the failed watch to leave the gate alone", got)
	}

	// The other half of the rule: a watch that WAS running and is then
	// replaced by one that fails must still release, or the key it had open
	// stays open with nothing watching it.
	monitor.mu.Lock()
	monitor.watchErr = nil
	monitor.mu.Unlock()
	if err := app.SetPTTKey("KeyN"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	monitor.fire(true)
	monitor.mu.Lock()
	monitor.watchErr = fmt.Errorf("%w: %q", hotkey.ErrUnknownKey, "KeyM")
	monitor.mu.Unlock()
	if err := app.SetPTTKey("KeyM"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	if got := voice.snapshot().ptt; len(got) == 0 || got[len(got)-1] {
		t.Fatalf("ptt = %v, want a release when a live watch is torn down", got)
	}

	// Once the key is one the platform knows, the message goes away.
	monitor.mu.Lock()
	monitor.watchErr = nil
	monitor.mu.Unlock()
	if err := app.SetPTTKey("KeyB"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	if got := app.HotkeyStatus().Error; got != "" {
		t.Fatalf("HotkeyStatus().Error = %q, want it cleared", got)
	}
}

func TestHotkeyErrorMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{hotkey.ErrUnknownKey, fmt.Sprintf(hotkeyUnknownKeyMessage, "KeyV")},
		{hotkey.ErrKeyUnavailable, fmt.Sprintf(hotkeyUnavailableMessage, "KeyV")},
		{hotkey.ErrUnsupported, hotkeyUnsupportedMessage},
		{errors.New("busy"), fmt.Sprintf(hotkeyRegisterMessage, "KeyV")},
	}
	for _, c := range cases {
		if got := hotkeyErrorMessage("KeyV", fmt.Errorf("wrapped: %w", c.err)); got != c.want {
			t.Errorf("hotkeyErrorMessage(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// delivery
// ---------------------------------------------------------------------------

// A toggle-only platform (Wayland) already alternates held and released, so
// core has nothing to do differently.
func TestGlobalPTTDeliversBothEdges(t *testing.T) {
	t.Parallel()
	for _, mode := range []hotkey.Mode{hotkey.ModeHold, hotkey.ModeToggle} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			_, voice, monitor := newHotkeyApp(t, gateWith(config.GateModePTT, true),
				hotkey.Capability{Mode: mode})

			monitor.fire(true)
			monitor.fire(false)
			monitor.fire(true)

			got := voice.snapshot().ptt
			if len(got) != 3 || !got[0] || got[1] || !got[2] {
				t.Fatalf("ptt = %v, want [true false true]", got)
			}
		})
	}
}

// internal/hotkey does not recover, and the callback runs on its polling
// goroutine: a panic there would take the whole client down with it.
func TestGlobalPTTSurvivesAPanickingCallback(t *testing.T) {
	t.Parallel()
	app := New(discardLogger(), nil)
	app.saver.after = newFakeClock().after
	app.SetVoice(&panicVoice{})
	cfg := config.Defaults()
	cfg.Gate = gateWith(config.GateModePTT, true)
	app.UseSettings("", cfg, nil)

	monitor := &fakeMonitor{capability: hotkey.Capability{Mode: hotkey.ModeHold}}
	app.SetKeyMonitor(monitor)

	monitor.fire(true)
	monitor.fire(false)

	// Still alive, and still watching: one broken transition is not a reason
	// to give up the key.
	if !monitor.isWatching() {
		t.Fatal("the watch did not survive the panic")
	}
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func TestHotkeyStatusReportsTheCapability(t *testing.T) {
	t.Parallel()
	app, _, _ := newHotkeyApp(t, gateWith(config.GateModePTT, true),
		hotkey.Capability{Mode: hotkey.ModeToggle, Reason: "На Wayland доступен только режим переключения"})

	status := app.HotkeyStatus()
	if status.Mode != "toggle" {
		t.Errorf("Mode = %q, want toggle", status.Mode)
	}
	if !strings.HasPrefix(status.Reason, "На Wayland") {
		t.Errorf("Reason = %q, want the capability text", status.Reason)
	}
}

func TestHotkeyStatusWithoutAMonitor(t *testing.T) {
	t.Parallel()
	app := New(discardLogger(), nil)
	if got := app.HotkeyStatus(); got.Mode != "unsupported" || got.Reason != "" || got.Error != "" {
		t.Fatalf("HotkeyStatus() = %+v, want an unsupported machine", got)
	}
}

// The settings can change while a key transition is being delivered: the UI
// runs on its own goroutines and the monitor polls on another. Stop waits for
// the callback in flight, so a core that held its state lock across it would
// deadlock here rather than fail.
func TestGlobalPTTIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	app, _, monitor := newHotkeyApp(t, gateWith(config.GateModePTT, true),
		hotkey.Capability{Mode: hotkey.ModeHold})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 40; n++ {
				app.SetGlobalPTT(n%2 == 0)
				if err := app.SetPTTKey(fmt.Sprintf("Key%c", 'A'+rune(i))); err != nil {
					t.Errorf("SetPTTKey: %v", err)
					return
				}
				app.HotkeyStatus()
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 200; n++ {
			monitor.fire(n%2 == 0)
		}
	}()
	wg.Wait()

	app.StopGlobalPTT()
	if monitor.isWatching() {
		t.Fatal("the watch outlived StopGlobalPTT")
	}
}
