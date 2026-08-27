package services

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// The services layer must stay a pass-through (PLAN.md §10.4). These tests
// pin the delegation: every call has to reach the controller unchanged, and
// no service may decide anything on its own.

type recorder struct {
	mu       sync.Mutex
	connects []string
	joins    []uint32
	sends    []string
	discos   int
	accepts  int
}

func (r *recorder) Connect(address, username, password string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connects = append(r.connects, address+"|"+username+"|"+password)
}
func (r *recorder) Disconnect() { r.mu.Lock(); defer r.mu.Unlock(); r.discos++ }
func (r *recorder) Join(id uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joins = append(r.joins, id)
	return nil
}
func (r *recorder) SendMessage(_ uint32, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, text)
	return nil
}
func (r *recorder) AcceptFingerprint()              { r.mu.Lock(); defer r.mu.Unlock(); r.accepts++ }
func (r *recorder) SetSelfAudio(bool, bool)         {}
func (r *recorder) SelfAudioSettled(bool, bool) bool { return true }
func (r *recorder) PreferTransport(string, string)  {}
func (r *recorder) Status() domain.ConnectionStatus { return domain.ConnectionStatus{} }
func (r *recorder) Close()                          {}

var _ mumble.Controller = (*recorder)(nil)

type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

func newApp(t *testing.T) (*core.App, *recorder) {
	t.Helper()
	rec := &recorder{}
	app := core.New(nil, nopEmitter{})
	app.SetController(rec)
	return app, rec
}

func TestConnectionServiceDelegates(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewConnectionService(app)

	if err := svc.Connect("localhost:64738", "bob", "pw"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := svc.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	svc.AcceptFingerprint()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.connects) != 1 || rec.connects[0] != "localhost:64738|bob|pw" {
		t.Errorf("connects = %v", rec.connects)
	}
	if rec.discos != 1 || rec.accepts != 1 {
		t.Errorf("discos=%d accepts=%d, want 1/1", rec.discos, rec.accepts)
	}
}

func TestConnectionServicePropagatesValidationError(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewConnectionService(app)

	if err := svc.Connect("localhost:64738", "  ", ""); !errors.Is(err, core.ErrEmptyUsername) {
		t.Fatalf("err = %v, want %v", err, core.ErrEmptyUsername)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.connects) != 0 {
		t.Fatalf("controller was called despite invalid input: %v", rec.connects)
	}
}

func TestConnectionServiceReportsState(t *testing.T) {
	t.Parallel()
	app, _ := newApp(t)
	svc := NewConnectionService(app)

	if got := svc.State(); got != string(domain.StateDisconnected) {
		t.Fatalf("State() = %q, want %q", got, domain.StateDisconnected)
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected, Server: "s"})

	if got := svc.State(); got != string(domain.StateConnected) {
		t.Fatalf("State() = %q, want %q", got, domain.StateConnected)
	}
	if got := svc.Status(); got.Server != "s" || got.State != domain.StateConnected {
		t.Fatalf("Status() = %+v", got)
	}
}

// The picker surface is a pass-through like everything else here: core owns
// the remembered list, the credential store and the decision to dial.
func TestConnectionServiceDelegatesTheServerPicker(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewConnectionService(app)

	if got := svc.Servers(); len(got) != 0 {
		t.Fatalf("Servers() = %+v, want none before any connect", got)
	}

	// A connect the server accepted is what fills the picker.
	if err := svc.Connect("localhost:64738", "bob", "pw"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})

	got := svc.Servers()
	if len(got) != 1 || got[0].Address != "localhost:64738" || got[0].Username != "bob" {
		t.Fatalf("Servers() = %+v", got)
	}
	// No credential store is injected here, so nothing claims to hold a
	// password - and none is exposed on the binding surface either way.
	if got[0].HasPassword {
		t.Errorf("HasPassword is true without a credential store")
	}

	result, err := svc.ConnectSaved("localhost:64738")
	if err != nil {
		t.Fatalf("ConnectSaved: %v", err)
	}
	if result.Reason != domain.SavedConnectStarted {
		t.Fatalf("result = %+v, want a started connect", result)
	}
	rec.mu.Lock()
	connects := append([]string(nil), rec.connects...)
	rec.mu.Unlock()
	if len(connects) != 2 || connects[1] != "localhost:64738|bob|" {
		t.Fatalf("connects = %v", connects)
	}

	if err := svc.ForgetServer("localhost:64738"); err != nil {
		t.Fatalf("ForgetServer: %v", err)
	}
	if got := svc.Servers(); len(got) != 0 {
		t.Fatalf("Servers() after forget = %+v", got)
	}
}

// An address that is no longer in the picker comes back as a reason the UI
// switches on, not as a sentence it has to match.
func TestConnectionServiceReportsAnUnknownServer(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewConnectionService(app)

	result, err := svc.ConnectSaved("stranger.example:64738")
	if err != nil {
		t.Fatalf("ConnectSaved: %v", err)
	}
	if result.Reason != domain.SavedConnectUnknown {
		t.Fatalf("reason = %q, want %q", result.Reason, domain.SavedConnectUnknown)
	}
	if result.Message == "" {
		t.Error("no message for the user")
	}
	if result.Address != "stranger.example:64738" {
		t.Errorf("address = %q, want the one that was clicked", result.Address)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.connects) != 0 {
		t.Fatalf("an unknown server was dialled: %v", rec.connects)
	}
}

func TestChannelsServiceDelegates(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewChannelsService(app)

	if err := svc.Join(7); err != nil {
		t.Fatalf("Join: %v", err)
	}
	rec.mu.Lock()
	joins := append([]uint32(nil), rec.joins...)
	rec.mu.Unlock()
	if len(joins) != 1 || joins[0] != 7 {
		t.Fatalf("joins = %v, want [7]", joins)
	}

	app.HandleTree(domain.ChannelNode{ID: 0, Name: "Root"})
	if got := svc.Tree(); got.Name != "Root" {
		t.Fatalf("Tree() = %+v", got)
	}
}

func TestChatServiceDelegates(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewChatService(app)

	if err := svc.Send(3, "  hello  "); err != nil {
		t.Fatalf("Send: %v", err)
	}
	rec.mu.Lock()
	sends := append([]string(nil), rec.sends...)
	rec.mu.Unlock()
	if len(sends) != 1 || sends[0] != "hello" {
		t.Fatalf("sends = %v, want [hello]", sends)
	}

	if err := svc.Send(3, "   "); !errors.Is(err, core.ErrEmptyMessage) {
		t.Fatalf("blank send err = %v, want %v", err, core.ErrEmptyMessage)
	}

	// A successful send is echoed into local history (the server never sends
	// our own text back), a rejected one is not.
	hist := svc.History(3)
	if len(hist) != 1 || hist[0].HTML != "hello" {
		t.Fatalf("History after send = %+v, want local echo [hello]", hist)
	}

	app.HandleMessage(mumble.RawMessage{ChannelID: 3, Sender: "bob", HTML: "<b>hi</b><script>x</script>"})
	hist = svc.History(3)
	if len(hist) != 2 || hist[1].HTML != "<b>hi</b>" {
		t.Fatalf("History = %+v", hist)
	}
	if got := svc.History(99); len(got) != 0 {
		t.Fatalf("History(99) = %+v, want empty", got)
	}
}

// voiceRecorder is the engine end of the audio plumbing: it records what core
// forwards, so the service tests can prove the call arrived unchanged.
type voiceRecorder struct {
	mu      sync.Mutex
	modes   []core.GateMode
	tunings []string
	ptt     []bool
	mutes   []bool
	deafens []bool
	volumes []string
	userMut []string
	cues    []core.Cue
	cueVols []float32
}

func (v *voiceRecorder) Start(string, string) error { return nil }
func (v *voiceRecorder) Stop()                      {}
func (v *voiceRecorder) SetUserVolume(hash string, volume float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.volumes = append(v.volumes, fmt.Sprintf("%s|%g", hash, volume))
}

func (v *voiceRecorder) SetUserMute(hash string, muted bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.userMut = append(v.userMut, fmt.Sprintf("%s|%v", hash, muted))
}

func (v *voiceRecorder) SetMute(muted bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mutes = append(v.mutes, muted)
}

func (v *voiceRecorder) SetDeafen(deafened bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.deafens = append(v.deafens, deafened)
}

func (v *voiceRecorder) SetCueVolume(volume float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cueVols = append(v.cueVols, volume)
}

func (v *voiceRecorder) PlayCue(cue core.Cue) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cues = append(v.cues, cue)
}

func (v *voiceRecorder) SetGateMode(mode core.GateMode) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.modes = append(v.modes, mode)
}

func (v *voiceRecorder) SetVADTuning(open, close float32, hangoverMs int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tunings = append(v.tunings, fmt.Sprintf("%g|%g|%d", open, close, hangoverMs))
}

func (v *voiceRecorder) SetPTT(held bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ptt = append(v.ptt, held)
}

func (v *voiceRecorder) Devices() (playback, capture []domain.AudioDevice, err error) {
	return nil, nil, nil
}

var _ core.VoiceEngine = (*voiceRecorder)(nil)

func newAudioApp(t *testing.T) (*core.App, *voiceRecorder) {
	t.Helper()
	app, _ := newApp(t)
	voice := &voiceRecorder{}
	app.SetVoice(voice)
	return app, voice
}

func TestAudioServiceGateControlsDelegate(t *testing.T) {
	t.Parallel()
	app, voice := newAudioApp(t)
	svc := NewAudioService(app)

	if err := svc.SetGateMode("ptt"); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	// float64 in, float32 at the engine: the values must survive the
	// narrowing, and the closing edge is derived rather than passed in.
	if err := svc.SetVADTuning(0.75, 250); err != nil {
		t.Fatalf("SetVADTuning: %v", err)
	}
	if err := svc.SetPTT(true); err != nil {
		t.Fatalf("SetPTT(true): %v", err)
	}
	if err := svc.SetPTT(false); err != nil {
		t.Fatalf("SetPTT(false): %v", err)
	}

	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.modes) != 1 || voice.modes[0] != core.GateModePTT {
		t.Errorf("modes = %v, want [%v]", voice.modes, core.GateModePTT)
	}
	if len(voice.tunings) != 1 || voice.tunings[0] != "0.75|0.55|250" {
		t.Errorf("tunings = %v, want [0.75|0.55|250]", voice.tunings)
	}
	if len(voice.ptt) != 2 || !voice.ptt[0] || voice.ptt[1] {
		t.Errorf("ptt = %v, want [true false]", voice.ptt)
	}
}

// The per-user controls cross the binding boundary as a hash plus a value.
// The gain narrows from the float64 the bindings marshal a JS number into;
// the mute is its own call, so silencing somebody never costs their gain.
func TestAudioServiceUserControlsDelegate(t *testing.T) {
	t.Parallel()
	app, voice := newAudioApp(t)
	svc := NewAudioService(app)

	svc.SetUserVolume("deadbeef", 0.4)
	svc.SetUserMute("deadbeef", true)
	svc.SetUserMute("deadbeef", false)

	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.volumes) != 1 || voice.volumes[0] != "deadbeef|0.4" {
		t.Errorf("volumes = %v, want [deadbeef|0.4]", voice.volumes)
	}
	if len(voice.userMut) != 2 || voice.userMut[0] != "deadbeef|true" || voice.userMut[1] != "deadbeef|false" {
		t.Errorf("mutes = %v, want [deadbeef|true deadbeef|false]", voice.userMut)
	}
}

func TestAudioServicePropagatesValidationErrors(t *testing.T) {
	t.Parallel()
	app, voice := newAudioApp(t)
	svc := NewAudioService(app)

	if err := svc.SetGateMode("whisper"); !errors.Is(err, core.ErrUnknownGateMode) {
		t.Errorf("SetGateMode err = %v, want %v", err, core.ErrUnknownGateMode)
	}
	if err := svc.SetVADTuning(1.5, 300); !errors.Is(err, core.ErrInvalidVADTuning) {
		t.Errorf("SetVADTuning err = %v, want %v", err, core.ErrInvalidVADTuning)
	}

	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.modes) != 0 || len(voice.tunings) != 0 {
		t.Fatalf("engine reached despite invalid input: modes=%v tunings=%v", voice.modes, voice.tunings)
	}
}

func TestSettingsServiceDelegates(t *testing.T) {
	t.Parallel()
	app, voice := newAudioApp(t)
	svc := NewSettingsService(app)
	audio := NewAudioService(app)

	// The snapshot is what the UI starts on, so it has to follow every path
	// that changes a setting - including the ones on other services.
	if got := svc.Load(); got.GateMode != string(core.GateModeVAD) || got.PttKey != "Space" ||
		got.CueVolume != config.DefaultCueVolume {
		t.Fatalf("Load() = %+v, want the defaults", got)
	}
	if err := audio.SetGateMode("ptt"); err != nil {
		t.Fatalf("SetGateMode: %v", err)
	}
	if err := audio.SetVADTuning(0.8, 700); err != nil {
		t.Fatalf("SetVADTuning: %v", err)
	}
	audio.SelectDevices("aa11", "bb22")
	if err := svc.SetPTTKey("KeyF"); err != nil {
		t.Fatalf("SetPTTKey: %v", err)
	}
	svc.SetGlobalPTT(true)
	if err := svc.SetCueVolume(0.25); err != nil {
		t.Fatalf("SetCueVolume: %v", err)
	}

	got := svc.Load()
	want := Settings{
		CaptureID: "aa11", PlaybackID: "bb22",
		GateMode: string(core.GateModePTT), VadOpen: 0.8, HangoverMs: 700, PttKey: "KeyF",
		GlobalPtt: true, CueVolume: 0.25,
		// No monitor is injected in a service test, so the machine reports
		// what a platform without one would.
		Hotkey: Hotkey{Mode: "unsupported"},
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
	// The gain reaches the engine, not just the document.
	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.cueVols) != 1 || voice.cueVols[0] != 0.25 {
		t.Fatalf("cue volumes at the engine = %v, want [0.25]", voice.cueVols)
	}
}

func TestSettingsServiceRejectsAnImpossibleCueVolume(t *testing.T) {
	t.Parallel()
	app, voice := newAudioApp(t)
	svc := NewSettingsService(app)

	if err := svc.SetCueVolume(1.5); !errors.Is(err, core.ErrInvalidCueVolume) {
		t.Fatalf("SetCueVolume err = %v, want %v", err, core.ErrInvalidCueVolume)
	}
	if got := svc.Load().CueVolume; got != config.DefaultCueVolume {
		t.Errorf("cue volume = %v, want it untouched", got)
	}
	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.cueVols) != 0 {
		t.Fatalf("engine reached despite invalid input: %v", voice.cueVols)
	}
}

// Mute and deafen are reachable from the window and from the system tray, so
// the service has to be a plain forward to the one place that owns the state -
// and it forwards a flip, not a value, so no caller ever has to read the state
// and then set its opposite.
func TestAudioServiceSelfStateDelegates(t *testing.T) {
	t.Parallel()
	app, voice := newAudioApp(t)
	svc := NewAudioService(app)

	if got := svc.SelfState(); got.Muted || got.Deafened {
		t.Fatalf("SelfState() = %+v, want everything on", got)
	}
	svc.ToggleMute()
	svc.ToggleDeafen()

	if got := svc.SelfState(); !got.Muted || !got.Deafened {
		t.Fatalf("SelfState() = %+v, want both off", got)
	}
	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.mutes) != 1 || !voice.mutes[0] {
		t.Errorf("mutes = %v, want [true]", voice.mutes)
	}
	if len(voice.deafens) != 1 || !voice.deafens[0] {
		t.Errorf("deafens = %v, want [true]", voice.deafens)
	}
	if len(voice.cues) != 1 || voice.cues[0] != core.CueMuted {
		t.Errorf("cues = %v, want [CueMuted]", voice.cues)
	}
}

func TestSettingsServicePropagatesValidationErrors(t *testing.T) {
	t.Parallel()
	app, _ := newAudioApp(t)
	svc := NewSettingsService(app)

	if err := svc.SetPTTKey("Ctrl+Q"); !errors.Is(err, core.ErrInvalidPTTKey) {
		t.Fatalf("SetPTTKey err = %v, want %v", err, core.ErrInvalidPTTKey)
	}
	if got := svc.Load().PttKey; got != "Space" {
		t.Fatalf("key = %q, want it untouched", got)
	}
}

func TestDiagnosticsServiceIsWired(t *testing.T) {
	t.Parallel()
	app, _ := newApp(t)
	if svc := NewDiagnosticsService(app); svc == nil || svc.app != app {
		t.Fatal("DiagnosticsService not wired to the core app")
	}
}
