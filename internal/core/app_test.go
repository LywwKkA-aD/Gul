package core

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"gul/internal/domain"
	"gul/internal/mumble"
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

func newTestApp(t *testing.T) (*App, *fakeController, *fakeEmitter) {
	t.Helper()
	em := &fakeEmitter{}
	ctrl := &fakeController{}
	app := New(slog.New(slog.NewTextHandler(io.Discard, nil)), em)
	app.SetController(ctrl)
	return app, ctrl, em
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
		{"long address", strings.Repeat("h", maxAddressLen+1), "bob", "", ErrEmptyAddress},
		{"empty username", "localhost:64738", "", "", ErrEmptyUsername},
		{"blank username", "localhost:64738", " \t ", "", ErrEmptyUsername},
		{"long username", "localhost:64738", strings.Repeat("n", maxUsernameLen+1), "", ErrEmptyUsername},
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
				switch (w + i) % 4 {
				case 0:
					app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
				case 1:
					app.HandleTree(domain.ChannelNode{ID: uint32(i), Name: "Root"})
				case 2:
					app.HandleMessage(mumble.RawMessage{ChannelID: uint32(w), HTML: "<b>x</b>"})
				case 3:
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

	if got := em.count(domain.EventConnectionState) + em.count(domain.EventChannelsTree) +
		em.count(domain.EventChatMessage) + em.count(domain.EventTofuMismatch); got != workers*iterations {
		t.Fatalf("emitted %d events, want %d", got, workers*iterations)
	}
}

func TestCallbacksBundle(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)

	cb := app.Callbacks()
	if cb.OnStatus == nil || cb.OnTree == nil || cb.OnMessage == nil || cb.OnTofu == nil {
		t.Fatal("Callbacks() left a hook nil")
	}
	cb.OnStatus(domain.ConnectionStatus{State: domain.StateConnecting})
	cb.OnTree(domain.ChannelNode{Name: "Root"})
	cb.OnMessage(mumble.RawMessage{ChannelID: 1, HTML: "hi"})
	cb.OnTofu(domain.TofuPrompt{Server: "s"})

	if got := len(em.all()); got != 4 {
		t.Fatalf("emitted %d events, want 4", got)
	}
}
