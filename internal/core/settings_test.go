package core

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/hotkey"
)

// ---------------------------------------------------------------------------
// doubles
// ---------------------------------------------------------------------------

// fakeClock closes the debounce window on demand, so the tests cost no wall
// clock time and cannot flake on a slow machine.
type fakeClock struct {
	mu      sync.Mutex
	next    int
	pending map[int]func()
	windows []time.Duration
}

func newFakeClock() *fakeClock { return &fakeClock{pending: map[int]func(){}} }

func (c *fakeClock) after(d time.Duration, fn func()) oneShotTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.next
	c.next++
	c.pending[id] = fn
	c.windows = append(c.windows, d)
	return &fakeOneShot{clock: c, id: id}
}

// fire runs every armed callback, oldest first.
func (c *fakeClock) fire() {
	c.mu.Lock()
	ids := make([]int, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	callbacks := make([]func(), 0, len(ids))
	for id := 0; id < c.next; id++ {
		if fn, ok := c.pending[id]; ok {
			callbacks = append(callbacks, fn)
			delete(c.pending, id)
		}
	}
	c.mu.Unlock()

	for _, fn := range callbacks {
		fn()
	}
}

func (c *fakeClock) armed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// arms is how many windows were opened over the lifetime of the clock.
func (c *fakeClock) arms() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.windows...)
}

type fakeOneShot struct {
	clock *fakeClock
	id    int
}

func (t *fakeOneShot) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, armed := t.clock.pending[t.id]
	delete(t.clock.pending, t.id)
	return armed
}

// orderedVoice records the engine calls in the order they arrive, which is
// what the startup sequence is about: the device selection has to be in place
// before Start, and the gate has to reach an engine that already exists.
type orderedVoice struct {
	mu    sync.Mutex
	calls []string
}

func (v *orderedVoice) record(format string, args ...any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, fmt.Sprintf(format, args...))
}

func (v *orderedVoice) snapshot() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.calls...)
}

func (v *orderedVoice) Start(captureID, playbackID string) error {
	v.record("start %s|%s", captureID, playbackID)
	return nil
}
func (v *orderedVoice) Stop()                         { v.record("stop") }
func (v *orderedVoice) SetMute(bool)                  {}
func (v *orderedVoice) SetDeafen(bool)                {}
func (v *orderedVoice) SetUserVolume(string, float32) {}
func (v *orderedVoice) SetPTT(bool)                   {}
func (v *orderedVoice) SetCueVolume(volume float32)   { v.record("cue volume %g", volume) }
func (v *orderedVoice) PlayCue(cue Cue)               { v.record("cue %d", cue) }
func (v *orderedVoice) SetGateMode(mode GateMode)     { v.record("gate %s", mode) }
func (v *orderedVoice) SetVADTuning(open, closeLevel float32, hangoverMs int) {
	v.record("vad %g/%g/%d", open, closeLevel, hangoverMs)
}
func (v *orderedVoice) Devices() (playback, capture []domain.AudioDevice, err error) {
	return nil, nil, nil
}

var _ VoiceEngine = (*orderedVoice)(nil)

// awaitCalls waits for the engine to have received want calls and gives a
// settling window on top, so one call too many is caught as well as one
// missing. Start and Stop run on goroutines of their own.
func awaitCalls(v *orderedVoice, want int) []string {
	const settle = 50 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := v.snapshot()
		if len(got) > want || time.Now().After(deadline) {
			return got
		}
		if len(got) == want {
			time.Sleep(settle)
			return v.snapshot()
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// newSettingsApp builds an App that persists into dir and whose debounce
// window the test closes by hand.
func newSettingsApp(t *testing.T, dir string, log *slog.Logger) (*App, *fakeClock) {
	t.Helper()
	app := New(log, nil)
	clock := newFakeClock()
	app.saver.after = clock.after
	cfg, err := config.Load(dir)
	app.UseSettings(dir, cfg, err)
	return app, clock
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(new(bytes.Buffer), nil))
}

// ---------------------------------------------------------------------------
// startup
// ---------------------------------------------------------------------------

func TestUseSettingsAppliesTheGateBeforeTheEngineStarts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stored := config.Defaults()
	stored.Audio = config.Audio{CaptureID: "aa11", PlaybackID: "bb22", CueVolume: 0.25}
	stored.Gate = config.Gate{Mode: config.GateModePTT, OpenThreshold: 0.75, HangoverMs: 500, PTTKey: "KeyF"}
	if err := config.Save(dir, stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	app := New(discardLogger(), nil)
	voice := &orderedVoice{}
	app.SetVoice(voice)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	app.UseSettings(dir, cfg, err)

	// The engine has the gate and the cue gain before anything is on the wire.
	if got := voice.snapshot(); len(got) != 3 || got[0] != "gate ptt" ||
		got[1] != "vad 0.75/0.55/500" || got[2] != "cue volume 0.25" {
		t.Fatalf("engine calls after UseSettings = %v", got)
	}

	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	got := awaitCalls(voice, 4)
	if len(got) != 4 {
		t.Fatalf("engine calls = %v, want the gate then one start", got)
	}
	// The stored devices are what the engine is started on: a selection
	// applied after Start would cost a restart on every launch.
	if got[3] != "start aa11|bb22" {
		t.Fatalf("start = %q, want the stored devices", got[3])
	}
}

func TestUseSettingsWithoutAnEngineKeepsTheSelection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stored := config.Defaults()
	stored.Audio = config.Audio{CaptureID: "cc33", PlaybackID: "dd44"}
	if err := config.Save(dir, stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// UseSettings before SetVoice: the gate has nowhere to go, but the
	// selection must still be what the engine is later started on.
	app := New(discardLogger(), nil)
	cfg, _ := config.Load(dir)
	app.UseSettings(dir, cfg, nil)
	voice := &orderedVoice{}
	app.SetVoice(voice)

	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	got := awaitCalls(voice, 1)
	if len(got) != 1 || got[0] != "start cc33|dd44" {
		t.Fatalf("engine calls = %v, want one start on the stored devices", got)
	}
}

// ---------------------------------------------------------------------------
// debounce and flush
// ---------------------------------------------------------------------------

func TestSettingsWritesAreDebounced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, clock := newSettingsApp(t, dir, discardLogger())

	var writes int
	saveSettings := app.saveSettings
	app.saver.write = func() {
		writes++
		saveSettings()
	}

	// A slider drag: many changes, one window.
	for _, open := range []float64{0.61, 0.62, 0.63, 0.64} {
		if err := app.SetVADTuning(open, 300); err != nil {
			t.Fatalf("SetVADTuning: %v", err)
		}
	}
	if got := clock.armed(); got != 1 {
		t.Fatalf("armed windows = %d, want 1", got)
	}
	if writes != 0 {
		t.Fatalf("writes = %d before the window closed", writes)
	}

	clock.fire()
	if writes != 1 {
		t.Fatalf("writes = %d, want 1 for the whole burst", writes)
	}
	if got := clock.arms(); len(got) != 1 || got[0] != settingsSaveWindow {
		t.Fatalf("windows = %v, want one of %v", got, settingsSaveWindow)
	}

	// Only the last value of the burst reaches the file.
	stored, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Gate.OpenThreshold != 0.64 {
		t.Fatalf("stored threshold = %v, want the last of the burst", stored.Gate.OpenThreshold)
	}

	// The window is over: the next change opens a new one.
	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	if got := clock.armed(); got != 1 {
		t.Fatalf("armed windows after the next change = %d, want 1", got)
	}
}

func TestUnchangedSettingsDoNotOpenAWindow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, clock := newSettingsApp(t, dir, discardLogger())

	// Setting what is already set is what a UI does on every render.
	if err := app.SetGateMode(string(config.GateModeVAD)); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	if err := app.SetVADTuning(config.DefaultOpenThreshold, config.DefaultHangoverMs); err != nil {
		t.Fatalf("SetVADTuning: %v", err)
	}
	app.SelectDevices("", "")
	app.RememberConnection("  ", "")

	if got := clock.armed(); got != 0 {
		t.Fatalf("armed windows = %d, want none", got)
	}
}

func TestFlushWritesWithoutWaitingForTheWindow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, clock := newSettingsApp(t, dir, discardLogger())

	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	app.FlushSettings()

	if got := clock.armed(); got != 0 {
		t.Fatalf("armed windows after flush = %d, want none", got)
	}
	stored, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Gate.PTTKey != "KeyF" {
		t.Fatalf("stored key = %q, want KeyF", stored.Gate.PTTKey)
	}

	// The disarmed window may not fire a second write afterwards.
	clock.fire()
}

func TestSettingsRoundTripThroughCore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, _ := newSettingsApp(t, dir, discardLogger())

	app.SelectDevices("aa11", "bb22")
	if err := app.SetGateMode("ptt"); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	if err := app.SetVADTuning(0.8, 700); err != nil {
		t.Fatalf("SetVADTuning: %v", err)
	}
	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	if err := app.SetCueVolume(0.25); err != nil {
		t.Fatalf("SetCueVolume: %v", err)
	}
	app.SetGlobalPTT(true)
	app.RememberConnection("  host:64738  ", " gul ")
	app.FlushSettings()

	next, _ := newSettingsApp(t, dir, discardLogger())
	got := next.Settings()
	want := config.Config{
		Version:    config.SchemaVersion,
		Connection: config.Connection{LastAddress: "host:64738", LastUsername: "gul"},
		Audio:      config.Audio{CaptureID: "aa11", PlaybackID: "bb22", CueVolume: 0.25},
		Gate: config.Gate{
			Mode: config.GateModePTT, OpenThreshold: 0.8, HangoverMs: 700,
			PTTKey: "KeyF", GlobalPTT: true,
		},
	}
	if got.Connection != want.Connection || got.Audio != want.Audio || got.Gate != want.Gate {
		t.Fatalf("restarted with %+v, want %+v", got, want)
	}
}

// ---------------------------------------------------------------------------
// mutators
// ---------------------------------------------------------------------------

func TestSetPTTKeyValidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, clock := newSettingsApp(t, dir, discardLogger())

	for _, code := range []string{"", "Ctrl+A", "Пробел", strings.Repeat("K", 64)} {
		if err := app.SetPTTKey(code); !errors.Is(err, ErrInvalidPTTKey) {
			t.Errorf("SetPTTKey(%q) = %v, want %v", code, err, ErrInvalidPTTKey)
		}
	}
	if got := app.Settings().Gate.PTTKey; got != config.DefaultPTTKey {
		t.Errorf("key = %q, want it untouched", got)
	}
	if got := clock.armed(); got != 0 {
		t.Fatalf("a rejected key opened a write window")
	}
}

func TestSetCueVolumeValidatesAndReachesTheEngine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, clock := newSettingsApp(t, dir, discardLogger())
	voice := &orderedVoice{}
	app.SetVoice(voice)

	for _, volume := range []float64{-0.1, 1.5, math.NaN()} {
		if err := app.SetCueVolume(volume); !errors.Is(err, ErrInvalidCueVolume) {
			t.Errorf("SetCueVolume(%v) = %v, want %v", volume, err, ErrInvalidCueVolume)
		}
	}
	if got := app.Settings().Audio.CueVolume; got != config.DefaultCueVolume {
		t.Errorf("cue volume = %v, want it untouched", got)
	}
	if got := clock.armed(); got != 0 {
		t.Fatal("a rejected cue volume opened a write window")
	}
	if got := voice.snapshot(); len(got) != 0 {
		t.Fatalf("engine calls = %v, want none for a rejected volume", got)
	}

	// Zero is a decision, not a missing value: it turns the cues off.
	if err := app.SetCueVolume(0); err != nil {
		t.Fatalf("SetCueVolume(0): %v", err)
	}
	if got := voice.snapshot(); len(got) != 1 || got[0] != "cue volume 0" {
		t.Fatalf("engine calls = %v, want the gain applied", got)
	}
	if got := clock.armed(); got != 1 {
		t.Fatalf("armed windows = %d, want the change to be written", got)
	}
}

// Shutdown is the last thing that runs: the key must stop being watched and
// the change made inside the debounce window must reach the file.
func TestShutdownStopsTheWatchAndWritesSettings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, clock := newSettingsApp(t, dir, discardLogger())
	voice := &fakeVoice{}
	app.SetVoice(voice)
	if err := app.SetGateMode(string(config.GateModePTT)); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	monitor := &fakeMonitor{capability: hotkey.Capability{Mode: hotkey.ModeHold}}
	app.SetKeyMonitor(monitor)
	app.SetGlobalPTT(true)
	monitor.fire(true)

	app.Shutdown()

	if monitor.isWatching() {
		t.Error("the key is still watched after shutdown")
	}
	if got := voice.snapshot().ptt; len(got) == 0 || got[len(got)-1] {
		t.Errorf("ptt = %v, want a release on the way out", got)
	}
	if got := clock.armed(); got != 0 {
		t.Errorf("armed windows = %d, want the pending write flushed", got)
	}
	stored, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !stored.Gate.GlobalPTT || stored.Gate.Mode != config.GateModePTT {
		t.Fatalf("stored gate = %+v, want what was set before shutdown", stored.Gate)
	}
}

// The connect form is remembered only once the server accepted it: a typo or
// a dead host must not be what comes back next time.
func TestConnectionIsRememberedOnlyAfterTheServerAccepts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, _ := newSettingsApp(t, dir, discardLogger())
	app.SetController(&fakeController{})

	if err := app.Connect("host:64738", "gul", "secret"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := app.Settings().Connection; got != (config.Connection{}) {
		t.Fatalf("connection = %+v before the server answered", got)
	}

	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	got := app.Settings().Connection
	if got.LastAddress != "host:64738" || got.LastUsername != "gul" {
		t.Fatalf("connection = %+v, want the accepted pair", got)
	}

	// An attempt that never connects leaves the remembered pair alone.
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	if err := app.Connect("typo.example.test:64738", "gul", ""); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := app.Settings().Connection.LastAddress; got != "host:64738" {
		t.Fatalf("address = %q, want the last accepted one", got)
	}
}

// Whatever is written must never carry the server password: the document
// lives on disk for as long as the installation does.
func TestTheStoredDocumentNeverCarriesThePassword(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, _ := newSettingsApp(t, dir, discardLogger())
	app.SetController(&fakeController{})

	if err := app.Connect("host:64738", "gul", "hunter2"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	app.FlushSettings()

	data, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if bytes.Contains(data, []byte("hunter2")) {
		t.Fatalf("the document carries the password:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// persistence failures
// ---------------------------------------------------------------------------

// A document written by a newer build is left exactly as it is: this session
// runs on defaults in memory, and nothing it changes goes over the file.
func TestANewerDocumentIsNeverOverwritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `{"version": 99, "gate": {"mode": "ptt"}}`
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	app, clock := newSettingsApp(t, dir, discardLogger())
	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	clock.fire()
	app.FlushSettings()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != body {
		t.Fatalf("the document was rewritten:\n%s", data)
	}
	// The session still works, it just does not outlive itself.
	if got := app.Settings().Gate.PTTKey; got != "KeyF" {
		t.Fatalf("in-memory key = %q, want KeyF", got)
	}
}

// An existing document that cannot be read is unknown, not empty: a flush
// must not replace it with defaults.
func TestAnUnreadableDocumentIsNeverOverwritten(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file permissions do not gate reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads regardless of permissions")
	}
	dir := t.TempDir()
	body := `{"version": 1, "gate": {"mode": "ptt"}}`
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o000); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	app, clock := newSettingsApp(t, dir, discardLogger())
	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	clock.fire()
	app.FlushSettings()

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != body {
		t.Fatalf("the unreadable document was rewritten:\n%s", data)
	}
}

// A configuration directory that cannot be written costs persistence, never
// the session - and it says so once, not on every change.
func TestUnwritableDirectoryIsReportedOnce(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not gate writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	logs := new(bytes.Buffer)
	app, _ := newSettingsApp(t, dir, slog.New(slog.NewTextHandler(logs, nil)))

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	app.FlushSettings()
	if err := app.SetPTTKey("KeyG"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	app.FlushSettings()

	if got := strings.Count(logs.String(), "settings not writable"); got != 1 {
		t.Fatalf("warnings = %d, want exactly 1:\n%s", got, logs)
	}
	if got := app.Settings().Gate.PTTKey; got != "KeyG" {
		t.Fatalf("in-memory key = %q, want the last change", got)
	}
}

// Without UseSettings there is no directory to write into: a core built for a
// test or a headless run must not create files anywhere.
func TestSettingsWithoutADirectoryStayInMemory(t *testing.T) {
	t.Parallel()
	app := New(discardLogger(), nil)
	if err := app.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	app.FlushSettings()
	if got := app.Settings().Gate.PTTKey; got != "KeyF" {
		t.Fatalf("key = %q, want KeyF", got)
	}
}
