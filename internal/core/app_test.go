package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// ---------------------------------------------------------------------------
// doubles
// ---------------------------------------------------------------------------

type emitted struct {
	name    string
	payload any
}

type fakeEmitter struct {
	mu     sync.Mutex
	events []emitted
}

func (e *fakeEmitter) Emit(name string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, emitted{name, payload})
}

func (e *fakeEmitter) all() []emitted {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]emitted, len(e.events))
	copy(out, e.events)
	return out
}

func (e *fakeEmitter) last() (emitted, bool) {
	all := e.all()
	if len(all) == 0 {
		return emitted{}, false
	}
	return all[len(all)-1], true
}

func (e *fakeEmitter) count(name string) int {
	n := 0
	for _, ev := range e.all() {
		if ev.name == name {
			n++
		}
	}
	return n
}

type connectCall struct{ address, username, password string }
type sendCall struct {
	channelID uint32
	text      string
}

type fakeController struct {
	mu sync.Mutex

	connects    []connectCall
	joins       []uint32
	sends       []sendCall
	disconnects int
	accepts     int
	closes      int

	joinErr error
	sendErr error
	status  domain.ConnectionStatus

	selfMutes       []bool
	selfDeafs       []bool
	selfAudioPnd    bool
	selfAudioEchoed bool
	preferred       []string
}

func (c *fakeController) SetSelfAudio(muted, deafened bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.selfMutes = append(c.selfMutes, muted)
	c.selfDeafs = append(c.selfDeafs, deafened)
	c.selfAudioEchoed = false
}

func (c *fakeController) PreferTransport(address, transport string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.preferred = append(c.preferred, address+"="+transport)
}

// The fake answers the same question the Manager does, and the same way: a
// tree is the server's opinion only once nothing of ours is in flight, AND once
// the pair it carries is the one we last sent. Anything else is a tree that
// crossed our packet on the wire.
func (c *fakeController) SelfAudioSettled(muted, deafened bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.selfAudioPnd {
		return false
	}
	if len(c.selfMutes) == 0 || c.selfAudioEchoed {
		return true
	}
	last := len(c.selfMutes) - 1
	if muted == c.selfMutes[last] && deafened == c.selfDeafs[last] {
		// The room has caught up. From here it speaks for itself again.
		c.selfAudioEchoed = true
		return true
	}
	return false
}

func (c *fakeController) setSelfAudioPending(pending bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.selfAudioPnd = pending
}

func (c *fakeController) Connect(address, username, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connects = append(c.connects, connectCall{address, username, password})
}

func (c *fakeController) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnects++
}

func (c *fakeController) Join(channelID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.joins = append(c.joins, channelID)
	return c.joinErr
}

func (c *fakeController) SendMessage(channelID uint32, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, sendCall{channelID, text})
	return c.sendErr
}

func (c *fakeController) AcceptFingerprint() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accepts++
}

func (c *fakeController) Status() domain.ConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *fakeController) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
}

func (c *fakeController) snapshot() fakeController {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fakeController{
		connects:    append([]connectCall(nil), c.connects...),
		joins:       append([]uint32(nil), c.joins...),
		sends:       append([]sendCall(nil), c.sends...),
		disconnects: c.disconnects,
		accepts:     c.accepts,
	}
}

var _ mumble.Controller = (*fakeController)(nil)

type vadTuning struct {
	open, close float32
	hangoverMs  int
}

// fakeVoice records what core forwards to the audio engine. Start/Stop run on
// their own goroutines in App, so every field is guarded.
type fakeVoice struct {
	mu sync.Mutex

	mutes    []bool
	deafens  []bool
	volumes  []volumeCall
	userMute []muteCall
	modes    []GateMode
	tunings  []vadTuning
	ptt      []bool
	cues     []Cue
	cueVols  []float32
	starts   int
	stops    int
	devCalls int
}

type volumeCall struct {
	hash   string
	volume float32
}

type muteCall struct {
	hash  string
	muted bool
}

func (v *fakeVoice) Start(string, string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.starts++
	return nil
}

func (v *fakeVoice) Stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stops++
}

func (v *fakeVoice) SetMute(muted bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mutes = append(v.mutes, muted)
}

func (v *fakeVoice) SetDeafen(deafened bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.deafens = append(v.deafens, deafened)
}

func (v *fakeVoice) SetUserVolume(hash string, volume float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.volumes = append(v.volumes, volumeCall{hash, volume})
}

func (v *fakeVoice) SetUserMute(hash string, muted bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.userMute = append(v.userMute, muteCall{hash, muted})
}

func (v *fakeVoice) SetGateMode(mode GateMode) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.modes = append(v.modes, mode)
}

func (v *fakeVoice) SetVADTuning(open, close float32, hangoverMs int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tunings = append(v.tunings, vadTuning{open, close, hangoverMs})
}

func (v *fakeVoice) SetPTT(held bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ptt = append(v.ptt, held)
}

func (v *fakeVoice) SetCueVolume(volume float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cueVols = append(v.cueVols, volume)
}

func (v *fakeVoice) PlayCue(cue Cue) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cues = append(v.cues, cue)
}

func (v *fakeVoice) Devices() (playback, capture []domain.AudioDevice, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.devCalls++
	return nil, nil, nil
}

func (v *fakeVoice) startCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.starts
}

func (v *fakeVoice) snapshot() fakeVoice {
	v.mu.Lock()
	defer v.mu.Unlock()
	return fakeVoice{
		mutes:    append([]bool(nil), v.mutes...),
		deafens:  append([]bool(nil), v.deafens...),
		volumes:  append([]volumeCall(nil), v.volumes...),
		userMute: append([]muteCall(nil), v.userMute...),
		modes:    append([]GateMode(nil), v.modes...),
		tunings:  append([]vadTuning(nil), v.tunings...),
		ptt:      append([]bool(nil), v.ptt...),
		cues:     append([]Cue(nil), v.cues...),
		cueVols:  append([]float32(nil), v.cueVols...),
	}
}

var _ VoiceEngine = (*fakeVoice)(nil)

func newTestApp(t *testing.T) (*App, *fakeController, *fakeEmitter) {
	t.Helper()
	em := &fakeEmitter{}
	ctrl := &fakeController{}
	app := New(slog.New(slog.NewTextHandler(io.Discard, nil)), em)
	app.SetController(ctrl)
	return app, ctrl, em
}

func newVoiceApp(t *testing.T) (*App, *fakeVoice) {
	t.Helper()
	app, _, _ := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)
	return app, voice
}

// ---------------------------------------------------------------------------
// commands
// ---------------------------------------------------------------------------

func TestNewStartsDisconnected(t *testing.T) {
	t.Parallel()
	app := New(nil, nil)
	if got := app.Status().State; got != domain.StateDisconnected {
		t.Fatalf("state = %q, want %q", got, domain.StateDisconnected)
	}
	if h := app.History(1); len(h) != 0 {
		t.Fatalf("history = %v, want empty", h)
	}
	// A nil emitter must not panic: unit tests and headless runs rely on it.
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnecting})
}

func TestConnectValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                        string
		address, username, password string
		wantErr                     error
	}{
		{"empty address", "", "bob", "", ErrEmptyAddress},
		{"blank address", "   ", "bob", "", ErrEmptyAddress},
		{"long address", strings.Repeat("h", config.MaxAddressLen+1), "bob", "", ErrEmptyAddress},
		{"empty username", "localhost:64738", "", "", ErrEmptyUsername},
		{"blank username", "localhost:64738", " \t ", "", ErrEmptyUsername},
		{"long username", "localhost:64738", strings.Repeat("n", config.MaxUsernameLen+1), "", ErrEmptyUsername},
		{"valid", "localhost:64738", "bob", "pw", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app, ctrl, _ := newTestApp(t)
			err := app.Connect(tc.address, tc.username, tc.password)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			want := 0
			if tc.wantErr == nil {
				want = 1
			}
			if got := len(ctrl.snapshot().connects); got != want {
				t.Fatalf("controller connects = %d, want %d", got, want)
			}
		})
	}
}

func TestConnectTrimsAndDelegates(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)

	if err := app.Connect("  localhost:64738 ", "  bob\n", "secret"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	got := ctrl.snapshot().connects
	if len(got) != 1 {
		t.Fatalf("connects = %d, want 1", len(got))
	}
	want := connectCall{"localhost:64738", "bob", "secret"}
	if got[0] != want {
		t.Fatalf("connect = %+v, want %+v", got[0], want)
	}
}

func TestConnectLogDoesNotContainUserInput(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	app := New(logger, &fakeEmitter{})
	app.SetController(&fakeController{})

	const addressMarker = "address-secret-marker"
	const usernameMarker = "username-private-marker"
	if err := app.Connect("wss://user:"+addressMarker+"@example.test/mumble", usernameMarker, "password-secret-marker"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	got := output.String()
	for _, forbidden := range []string{addressMarker, usernameMarker, "password-secret-marker"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("connect log contains user input %q", forbidden)
		}
	}
	if !strings.Contains(got, "connect requested") {
		t.Fatal("connect lifecycle event was not logged")
	}
}

func TestConnectResetsHistory(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	app.HandleMessage(mumble.RawMessage{ChannelID: 7, Sender: "bob", HTML: "hi"})
	if len(app.History(7)) != 1 {
		t.Fatal("message not stored")
	}
	if err := app.Connect("localhost:64738", "bob", ""); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if h := app.History(7); len(h) != 0 {
		t.Fatalf("history after reconnect = %v, want empty", h)
	}
}

func TestCommandsRequireController(t *testing.T) {
	t.Parallel()
	app := New(nil, &fakeEmitter{})

	if err := app.Connect("localhost:64738", "bob", ""); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Connect err = %v, want %v", err, ErrNotConnected)
	}
	if err := app.Join(3); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Join err = %v, want %v", err, ErrNotConnected)
	}
	if err := app.SendMessage(3, "hi"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("SendMessage err = %v, want %v", err, ErrNotConnected)
	}
	// These are best-effort and must stay silent without a controller.
	app.Disconnect()
	app.AcceptFingerprint()
}

func TestDisconnectJoinAcceptDelegate(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)

	app.Disconnect()
	app.AcceptFingerprint()
	if err := app.Join(42); err != nil {
		t.Fatalf("Join: %v", err)
	}

	snap := ctrl.snapshot()
	if snap.disconnects != 1 || snap.accepts != 1 {
		t.Fatalf("disconnects=%d accepts=%d, want 1/1", snap.disconnects, snap.accepts)
	}
	if len(snap.joins) != 1 || snap.joins[0] != 42 {
		t.Fatalf("joins = %v, want [42]", snap.joins)
	}
}

func TestJoinPropagatesError(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	sentinel := errors.New("permission denied")
	ctrl.joinErr = sentinel

	if err := app.Join(1); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestSendMessageValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		text     string
		wantErr  error
		wantSent string
	}{
		{"empty", "", ErrEmptyMessage, ""},
		{"spaces", "   ", ErrEmptyMessage, ""},
		{"tabs and newlines", "\t\n\r ", ErrEmptyMessage, ""},
		{"too long", strings.Repeat("x", maxOutgoingText+1), ErrMessageTooBig, ""},
		{"at limit", strings.Repeat("x", maxOutgoingText), nil, strings.Repeat("x", maxOutgoingText)},
		{"trimmed", "  hello  ", nil, "hello"},
		{"multibyte counted by rune", strings.Repeat("ж", maxOutgoingText), nil, strings.Repeat("ж", maxOutgoingText)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app, ctrl, _ := newTestApp(t)
			err := app.SendMessage(5, tc.text)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			sends := ctrl.snapshot().sends
			if tc.wantErr != nil {
				if len(sends) != 0 {
					t.Fatalf("sent %d messages, want 0", len(sends))
				}
				return
			}
			if len(sends) != 1 || sends[0].channelID != 5 || sends[0].text != tc.wantSent {
				t.Fatalf("sends = %+v, want channel 5 / %q", sends, tc.wantSent)
			}
		})
	}
}

func TestSendMessagePropagatesError(t *testing.T) {
	t.Parallel()
	app, ctrl, _ := newTestApp(t)
	sentinel := errors.New("no permission")
	ctrl.sendErr = sentinel

	if err := app.SendMessage(1, "hi"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

// ---------------------------------------------------------------------------
// voice
// ---------------------------------------------------------------------------

func TestVoiceGatesAndVolumeDelegate(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)

	app.SetMute(true)
	app.SetDeafen(true)
	app.SetUserVolume("deadbeef", 1.5)

	snap := voice.snapshot()
	if len(snap.mutes) != 1 || !snap.mutes[0] {
		t.Errorf("mutes = %v, want [true]", snap.mutes)
	}
	if len(snap.deafens) != 1 || !snap.deafens[0] {
		t.Errorf("deafens = %v, want [true]", snap.deafens)
	}
	if len(snap.volumes) != 1 || snap.volumes[0] != (volumeCall{"deadbeef", 1.5}) {
		t.Errorf("volumes = %v, want [{deadbeef 1.5}]", snap.volumes)
	}
}

// Local mute is a separate fact from the gain and reaches the engine as one:
// core forwards it and nothing else, because the engine is where the gain is
// kept for the unmute (internal/audio/users.go).
func TestSetUserMuteDelegates(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)

	app.SetUserVolume("deadbeef", 0.4)
	app.SetUserMute("deadbeef", true)
	app.SetUserMute("deadbeef", false)

	snap := voice.snapshot()
	want := []muteCall{{"deadbeef", true}, {"deadbeef", false}}
	if len(snap.userMute) != len(want) || snap.userMute[0] != want[0] || snap.userMute[1] != want[1] {
		t.Fatalf("mutes = %v, want %v", snap.userMute, want)
	}
	// Muting does not touch the gain on the way through.
	if len(snap.volumes) != 1 || snap.volumes[0] != (volumeCall{"deadbeef", 0.4}) {
		t.Errorf("volumes = %v, want the single change the user made", snap.volumes)
	}
}

// A core with no engine yet (the connect screen) must not panic on a mute.
func TestSetUserMuteWithoutAnEngineIsANoOp(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	app.SetUserMute("deadbeef", true)
}

func TestSetGateModeDelegates(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)

	if err := app.SetGateMode("ptt"); err != nil {
		t.Fatalf("SetGateMode(ptt): %v", err)
	}
	if err := app.SetGateMode("vad"); err != nil {
		t.Fatalf("SetGateMode(vad): %v", err)
	}

	got := voice.snapshot().modes
	want := []GateMode{GateModePTT, GateModeVAD}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("modes = %v, want %v", got, want)
	}
}

func TestSetGateModeRejectsUnknown(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "VAD", "Ptt", "off", "push-to-talk", " vad"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			app, voice := newVoiceApp(t)

			if err := app.SetGateMode(mode); !errors.Is(err, ErrUnknownGateMode) {
				t.Fatalf("err = %v, want %v", err, ErrUnknownGateMode)
			}
			if got := voice.snapshot().modes; len(got) != 0 {
				t.Fatalf("engine was told %v despite invalid input", got)
			}
		})
	}
}

func TestSetVADTuningDelegates(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)

	// Only the opening edge is asked of the user; the closing edge trails it
	// by the hysteresis band (config.CloseThreshold).
	if err := app.SetVADTuning(0.7, 250); err != nil {
		t.Fatalf("SetVADTuning: %v", err)
	}
	got := voice.snapshot().tunings
	want := vadTuning{0.7, 0.5, 250}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("tunings = %v, want [%v]", got, want)
	}
}

func TestSetVADTuningValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		open       float64
		hangoverMs int
		wantErr    bool
	}{
		{"defaults", 0.6, 300, false},
		{"always open", 0, 0, false},
		{"never open", 1, config.MaxHangoverMs, false},
		{"open above one", 1.01, 300, true},
		{"open below zero", -0.01, 300, true},
		{"open is NaN", math.NaN(), 300, true},
		{"open is Inf", math.Inf(1), 300, true},
		{"negative hangover", 0.6, -1, true},
		{"hangover past the cap", 0.6, config.MaxHangoverMs + 1, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app, voice := newVoiceApp(t)

			err := app.SetVADTuning(tc.open, tc.hangoverMs)
			if tc.wantErr && !errors.Is(err, ErrInvalidVADTuning) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidVADTuning)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			want := 1
			if tc.wantErr {
				want = 0
			}
			if got := len(voice.snapshot().tunings); got != want {
				t.Fatalf("engine calls = %d, want %d", got, want)
			}
		})
	}
}

func TestSetPTTDelegatesEveryTransition(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)

	// Repeats are the frontend's job to filter; core forwards what it is told.
	app.SetPTT(true)
	app.SetPTT(true)
	app.SetPTT(false)

	got := voice.snapshot().ptt
	want := []bool{true, true, false}
	if len(got) != len(want) {
		t.Fatalf("ptt = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ptt = %v, want %v", got, want)
		}
	}
}

func TestGateControlsWithoutEngineValidateAndStaySilent(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	// No engine injected: valid input is accepted and dropped, invalid input
	// is still rejected - validation belongs to core, not to the engine.
	if err := app.SetGateMode("ptt"); err != nil {
		t.Errorf("SetGateMode without engine = %v, want nil", err)
	}
	if err := app.SetGateMode("nope"); !errors.Is(err, ErrUnknownGateMode) {
		t.Errorf("SetGateMode(nope) = %v, want %v", err, ErrUnknownGateMode)
	}
	if err := app.SetVADTuning(0.6, 300); err != nil {
		t.Errorf("SetVADTuning without engine = %v, want nil", err)
	}
	if err := app.SetVADTuning(2, 300); !errors.Is(err, ErrInvalidVADTuning) {
		t.Errorf("SetVADTuning(2, ...) = %v, want %v", err, ErrInvalidVADTuning)
	}
	app.SetPTT(true)
	app.SetMute(true)
}

func TestParseGateMode(t *testing.T) {
	t.Parallel()

	if got, err := ParseGateMode("vad"); err != nil || got != GateModeVAD {
		t.Errorf("ParseGateMode(vad) = %q, %v", got, err)
	}
	if got, err := ParseGateMode("ptt"); err != nil || got != GateModePTT {
		t.Errorf("ParseGateMode(ptt) = %q, %v", got, err)
	}
	got, err := ParseGateMode("loud")
	if !errors.Is(err, ErrUnknownGateMode) {
		t.Errorf("err = %v, want %v", err, ErrUnknownGateMode)
	}
	if got != "" {
		t.Errorf("mode = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), `"loud"`) {
		t.Errorf("error %q does not name the rejected mode", err)
	}
}

// ---------------------------------------------------------------------------
// callbacks
// ---------------------------------------------------------------------------

func TestHandleStatusStoresAndEmits(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	want := domain.ConnectionStatus{
		State: domain.StateConnected, Server: "localhost:64738", SelfSession: 9, SelfChannel: 3,
	}
	app.HandleStatus(want)

	if got := app.Status(); got != want {
		t.Fatalf("Status() = %+v, want %+v", got, want)
	}
	ev, ok := em.last()
	if !ok || ev.name != domain.EventConnectionState {
		t.Fatalf("last event = %+v, want %s", ev, domain.EventConnectionState)
	}
	if got, ok := ev.payload.(domain.ConnectionStatus); !ok || got != want {
		t.Fatalf("payload = %#v, want %+v", ev.payload, want)
	}
}

func TestHandleLatencyEmitsWithoutChangingConnectionState(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	app.HandleStatus(domain.ConnectionStatus{
		State: domain.StateConnected, Server: "localhost:64738", SelfSession: 9,
	})

	want := domain.ConnectionLatency{PingMS: 28.6}
	app.HandleLatency(want)

	if got := app.Status(); got.State != domain.StateConnected || got.SelfSession != 9 {
		t.Fatalf("Status() changed after latency update: %+v", got)
	}
	ev, ok := em.last()
	if !ok || ev.name != domain.EventConnectionLatency {
		t.Fatalf("last event = %+v, want %s", ev, domain.EventConnectionLatency)
	}
	if got, ok := ev.payload.(domain.ConnectionLatency); !ok || got != want {
		t.Fatalf("payload = %#v, want %+v", ev.payload, want)
	}
}

func TestHandleTreeStoresAndEmits(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	root := domain.ChannelNode{
		ID: 0, Name: "Root",
		Users:    []domain.UserInfo{{Session: 1, Name: "bob", IsSelf: true}},
		Children: []domain.ChannelNode{{ID: 1, Name: "Games"}},
	}
	app.HandleTree(root)

	if got := app.Tree(); got.Name != "Root" || len(got.Children) != 1 {
		t.Fatalf("Tree() = %+v", got)
	}
	ev, _ := em.last()
	if ev.name != domain.EventChannelsTree {
		t.Fatalf("event = %q, want %q", ev.name, domain.EventChannelsTree)
	}
	if _, ok := ev.payload.(domain.ChannelNode); !ok {
		t.Fatalf("payload type = %T, want domain.ChannelNode", ev.payload)
	}
}

func TestHandleTofuEmitsWithoutStoringHistory(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	p := domain.TofuPrompt{Server: "localhost:64738", OldFingerprint: "aa", NewFingerprint: "bb"}
	app.HandleTofu(p)

	ev, _ := em.last()
	if ev.name != domain.EventTofuMismatch {
		t.Fatalf("event = %q, want %q", ev.name, domain.EventTofuMismatch)
	}
	if got, ok := ev.payload.(domain.TofuPrompt); !ok || got != p {
		t.Fatalf("payload = %#v, want %+v", ev.payload, p)
	}
}

func TestHandleMessageSanitizesStoresEmits(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	app.HandleMessage(mumble.RawMessage{
		ChannelID:  3,
		Sender:     "eve",
		SenderHash: "deadbeef",
		HTML:       `<b>hi</b><script>alert(1)</script><img src=x onerror=alert(1)>`,
	})

	hist := app.History(3)
	if len(hist) != 1 {
		t.Fatalf("history = %d, want 1", len(hist))
	}
	msg := hist[0]
	if msg.HTML != "<b>hi</b>" {
		t.Fatalf("HTML = %q, want %q", msg.HTML, "<b>hi</b>")
	}
	if msg.Sender != "eve" || msg.SenderHash != "deadbeef" || msg.ChannelID != 3 {
		t.Fatalf("message = %+v", msg)
	}
	if msg.ID == "" {
		t.Fatal("empty message id")
	}
	if msg.At.IsZero() {
		t.Fatal("zero timestamp")
	}

	ev, _ := em.last()
	if ev.name != domain.EventChatMessage {
		t.Fatalf("event = %q, want %q", ev.name, domain.EventChatMessage)
	}
	if got, ok := ev.payload.(domain.ChatMessage); !ok || got.HTML != msg.HTML || got.ID != msg.ID {
		t.Fatalf("payload = %#v, want %+v", ev.payload, msg)
	}
}

// ---------------------------------------------------------------------------
// history
// ---------------------------------------------------------------------------

func TestHistoryKeepsOrder(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	for i := range 10 {
		app.HandleMessage(mumble.RawMessage{ChannelID: 1, Sender: "bob", HTML: fmt.Sprint(i)})
	}
	hist := app.History(1)
	if len(hist) != 10 {
		t.Fatalf("len = %d, want 10", len(hist))
	}
	for i, m := range hist {
		if m.HTML != fmt.Sprint(i) {
			t.Fatalf("history[%d] = %q, want %q", i, m.HTML, fmt.Sprint(i))
		}
	}
}

func TestHistoryEvictsOldestAtCap(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	const extra = 25
	for i := range historyPerChannel + extra {
		app.HandleMessage(mumble.RawMessage{ChannelID: 1, Sender: "bob", HTML: fmt.Sprint(i)})
	}

	hist := app.History(1)
	if len(hist) != historyPerChannel {
		t.Fatalf("len = %d, want %d", len(hist), historyPerChannel)
	}
	if hist[0].HTML != fmt.Sprint(extra) {
		t.Fatalf("oldest = %q, want %q", hist[0].HTML, fmt.Sprint(extra))
	}
	last := fmt.Sprint(historyPerChannel + extra - 1)
	if hist[len(hist)-1].HTML != last {
		t.Fatalf("newest = %q, want %q", hist[len(hist)-1].HTML, last)
	}
}

func TestHistoryIsPerChannel(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	app.HandleMessage(mumble.RawMessage{ChannelID: 1, HTML: "one"})
	app.HandleMessage(mumble.RawMessage{ChannelID: 2, HTML: "two"})
	app.HandleMessage(mumble.RawMessage{ChannelID: 1, HTML: "three"})

	if got := len(app.History(1)); got != 2 {
		t.Fatalf("channel 1 = %d messages, want 2", got)
	}
	if got := len(app.History(2)); got != 1 {
		t.Fatalf("channel 2 = %d messages, want 1", got)
	}
	if got := len(app.History(99)); got != 0 {
		t.Fatalf("unknown channel = %d messages, want 0", got)
	}
}

func TestHistoryReturnsCopy(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	app.HandleMessage(mumble.RawMessage{ChannelID: 1, HTML: "original"})
	snap := app.History(1)
	snap[0].HTML = "tampered"

	if got := app.History(1)[0].HTML; got != "original" {
		t.Fatalf("stored message mutated through the snapshot: %q", got)
	}
}

func TestMessageIDsAreUnique(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	const n = 2000
	for range n {
		app.HandleMessage(mumble.RawMessage{ChannelID: 1, HTML: "x"})
	}
	// The cap keeps only the tail, so collect ids from the emitted events.
	seen := make(map[string]bool, n)
	for _, m := range app.History(1) {
		if seen[m.ID] {
			t.Fatalf("duplicate id %q", m.ID)
		}
		seen[m.ID] = true
	}
	if len(seen) != historyPerChannel {
		t.Fatalf("unique ids = %d, want %d", len(seen), historyPerChannel)
	}
}

// ---------------------------------------------------------------------------
// concurrency (go test -race)
// ---------------------------------------------------------------------------

func TestCallbacksAreConcurrencySafe(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range iterations {
				switch (w + i) % 5 {
				case 0:
					app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
				case 1:
					app.HandleLatency(domain.ConnectionLatency{PingMS: float64(i)})
				case 2:
					app.HandleTree(domain.ChannelNode{ID: uint32(i), Name: "Root"})
				case 3:
					app.HandleMessage(mumble.RawMessage{ChannelID: uint32(w), HTML: "<b>x</b>"})
				case 4:
					app.HandleTofu(domain.TofuPrompt{Server: "s"})
				}
			}
		}(w)
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				_ = app.Status()
				_ = app.Tree()
				_ = app.History(1)
			}
		}()
	}
	wg.Wait()

	if got := em.count(domain.EventConnectionState) + em.count(domain.EventConnectionLatency) +
		em.count(domain.EventChannelsTree) +
		em.count(domain.EventChatMessage) + em.count(domain.EventTofuMismatch); got != workers*iterations {
		t.Fatalf("emitted %d events, want %d", got, workers*iterations)
	}
}

func TestCallbacksBundle(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	cb := app.Callbacks()
	if cb.OnStatus == nil || cb.OnLatency == nil || cb.OnTree == nil || cb.OnMessage == nil || cb.OnTofu == nil {
		t.Fatal("Callbacks() left a hook nil")
	}
	cb.OnStatus(domain.ConnectionStatus{State: domain.StateConnecting})
	cb.OnLatency(domain.ConnectionLatency{PingMS: 12.5})
	cb.OnTree(domain.ChannelNode{Name: "Root"})
	cb.OnMessage(mumble.RawMessage{ChannelID: 1, HTML: "hi"})
	cb.OnTofu(domain.TofuPrompt{Server: "s"})

	if got := len(em.all()); got != 5 {
		t.Fatalf("emitted %d events, want 5", got)
	}
}

// TestHandleStatusLogDoesNotContainTheServerAddress guards the other half of
// the privacy contract: the status the UI receives keeps the address the user
// typed, the log record that mirrors it must not - and neither must the error
// text, which carries host:port from the network layer.
func TestHandleStatusLogDoesNotContainTheServerAddress(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	emitter := &fakeEmitter{}
	app := New(logger, emitter)

	const host = "murmur.private.test"
	status := domain.ConnectionStatus{
		State:  domain.StateDisconnected,
		Server: "wss://" + host + "/mumble",
		Error:  "dial wss://" + host + "/mumble: dial tcp " + host + ":443: connect: connection refused",
	}
	app.HandleStatus(status)

	logged := output.String()
	if strings.Contains(logged, host) {
		t.Fatalf("connection state log leaked the server address: %s", logged)
	}
	if !strings.Contains(logged, "connection refused") {
		t.Fatalf("connection state log lost the diagnosis: %s", logged)
	}
	// The UI still needs the address it was given.
	event, ok := emitter.last()
	if !ok {
		t.Fatal("no status was emitted")
	}
	if got := event.payload.(domain.ConnectionStatus); got != status {
		t.Fatalf("emitted status = %+v, want %+v", got, status)
	}
}

// TestHandleStatusStartsVoiceOncePerSession pins which lifecycle transition
// owns the audio engine, and with it the cost of getting the connection state
// wrong. A first connect that had to wait out a relay refusal (429/503) stays
// "connecting" until it succeeds: the engine has never run, so the 'connected'
// that follows has to start it. A reconnect is the opposite case - the engine
// is already running and the transport channel survives the drop - so it must
// not be started a second time.
func TestHandleStatusStartsVoiceOncePerSession(t *testing.T) {
	const server = "wss://murmur.example.test/mumble"

	cases := []struct {
		name       string
		states     []domain.ConnectionStatus
		wantStarts int
	}{
		{
			name: "first connect that waited out a relay refusal",
			states: []domain.ConnectionStatus{
				{State: domain.StateConnecting, Server: server},
				{State: domain.StateConnecting, Server: server, Error: "Следующая попытка через 30 секунд."},
				{State: domain.StateConnecting, Server: server},
				{State: domain.StateConnected, Server: server},
			},
			wantStarts: 1,
		},
		{
			name: "reconnect after an unexpected drop",
			states: []domain.ConnectionStatus{
				{State: domain.StateReconnecting, Server: server},
				{State: domain.StateConnected, Server: server},
			},
			wantStarts: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, voice := newVoiceApp(t)
			for _, status := range tc.states {
				app.HandleStatus(status)
			}

			if got := awaitVoiceStarts(voice, tc.wantStarts); got != tc.wantStarts {
				t.Fatalf("voice starts = %d, want %d", got, tc.wantStarts)
			}
		})
	}
}

// awaitVoiceStarts returns how many times the engine was started, once the
// expected number has arrived and a settling window has passed on top - so a
// start too many is caught as reliably as a start missing. startVoice runs on
// a goroutine of its own, which is what makes the wait necessary.
func awaitVoiceStarts(v *fakeVoice, want int) int {
	const settle = 50 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := v.startCount(); got > want || time.Now().After(deadline) {
			return got
		} else if got == want {
			time.Sleep(settle)
			return v.startCount()
		}
		time.Sleep(2 * time.Millisecond)
	}
}
