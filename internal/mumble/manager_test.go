package mumble

import (
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"gul/internal/domain"
)

// statusSink collects OnStatus callbacks. The buffer is generous because a
// blocked callback would stall the Manager, which is exactly what the
// production contract forbids.
type statusSink struct {
	ch chan domain.ConnectionStatus
}

func newStatusSink() *statusSink {
	return &statusSink{ch: make(chan domain.ConnectionStatus, 64)}
}

func (s *statusSink) record(status domain.ConnectionStatus) { s.ch <- status }

func (s *statusSink) next(t *testing.T) domain.ConnectionStatus {
	t.Helper()
	select {
	case status := <-s.ch:
		return status
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a status callback")
		return domain.ConnectionStatus{}
	}
}

func (s *statusSink) expect(t *testing.T, want domain.ConnState) domain.ConnectionStatus {
	t.Helper()
	status := s.next(t)
	if status.State != want {
		t.Fatalf("state = %q (error %q), want %q", status.State, status.Error, want)
	}
	return status
}

// newTestManager builds a Manager with the network and the backoff replaced.
// Callers set dialFn before Connect, so the run goroutine never races with it.
func newTestManager(t *testing.T, cb Callbacks) *Manager {
	t.Helper()

	m, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), cb)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.backoffFn = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(m.Close)
	return m
}

func TestManagerStartsDisconnected(t *testing.T) {
	m := newTestManager(t, Callbacks{})

	if got := m.Status().State; got != domain.StateDisconnected {
		t.Fatalf("initial state = %q, want %q", got, domain.StateDisconnected)
	}
}

func TestManagerRejectsMissingUsername(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		t.Error("dial must not be attempted without a username")
		return nil, errors.New("unreachable")
	}

	m.Connect("localhost", "  ", "")

	status := sink.expect(t, domain.StateDisconnected)
	if status.Error == "" {
		t.Fatal("the rejection must explain itself")
	}
}

func TestManagerFirstAttemptFailureIsTerminal(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		attempts.Add(1)
		return nil, errors.New("connection refused")
	}

	m.Connect("localhost", "gul", "")

	sink.expect(t, domain.StateConnecting)
	status := sink.expect(t, domain.StateDisconnected)
	if status.Error != "connection refused" {
		t.Fatalf("error = %q, want the dial error verbatim", status.Error)
	}
	if status.Server != "localhost:64738" {
		t.Fatalf("server = %q, want the normalized address", status.Server)
	}

	// A user staring at a connect form needs the error, not a silent retry.
	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want exactly 1", got)
	}
}

func TestManagerReconnectsAfterUnexpectedDrop(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	hooksCh := make(chan sessionHooks, 4)
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		hooksCh <- hooks
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")

	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError, String: "eof"})

	sink.expect(t, domain.StateReconnecting)
	sink.expect(t, domain.StateConnected)

	if _, ok := <-hooksCh; !ok {
		t.Fatal("expected a second dial")
	}
}

func TestManagerStopsReconnectingAfterKick(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	hooksCh := make(chan sessionHooks, 4)
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		attempts.Add(1)
		hooksCh <- hooks
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectKicked, String: "spam"})

	status := sink.expect(t, domain.StateDisconnected)
	if status.Error != "kicked: spam" {
		t.Fatalf("error = %q, want the kick reason", status.Error)
	}

	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want 1: a kick must not be retried", got)
	}
}

func TestManagerStopsReconnectingOnRejectedCredentials(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	hooksCh := make(chan sessionHooks, 4)
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		if attempts.Add(1) == 1 {
			hooksCh <- hooks
			return &Session{}, nil
		}
		return nil, &gumble.RejectError{Type: gumble.RejectServerPassword}
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError})

	sink.expect(t, domain.StateReconnecting)
	status := sink.expect(t, domain.StateDisconnected)
	if status.Error == "" {
		t.Fatal("a rejected reconnect must report why")
	}

	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 2 {
		t.Fatalf("dial attempts = %d, want 2: bad credentials cannot fix themselves", got)
	}
}

func TestManagerRetriesTransientRejects(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	hooksCh := make(chan sessionHooks, 8)
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		switch attempts.Add(1) {
		case 1:
			hooksCh <- hooks
			return &Session{}, nil
		case 2:
			// The server has not reaped our previous session yet.
			return nil, &gumble.RejectError{Type: gumble.RejectUsernameInUse}
		default:
			hooksCh <- hooks
			return &Session{}, nil
		}
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError})

	sink.expect(t, domain.StateReconnecting)
	sink.expect(t, domain.StateReconnecting)
	sink.expect(t, domain.StateConnected)
}

func TestManagerPromptsAndRetriesOnFingerprintChange(t *testing.T) {
	sink := newStatusSink()
	prompts := make(chan domain.TofuPrompt, 4)
	m := newTestManager(t, Callbacks{
		OnStatus: sink.record,
		OnTofu:   func(p domain.TofuPrompt) { prompts <- p },
	})

	// The host was trusted once; now it presents something else.
	if err := m.tofu.verify("localhost", "old-fingerprint"); err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int32
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		if attempts.Add(1) == 1 {
			return nil, m.tofu.verify("localhost", "new-fingerprint")
		}
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")

	sink.expect(t, domain.StateConnecting)
	if status := sink.expect(t, domain.StateDisconnected); status.Error == "" {
		t.Fatal("the mismatch must be reported in the status too")
	}

	var prompt domain.TofuPrompt
	select {
	case prompt = <-prompts:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the TOFU prompt")
	}
	if prompt.OldFingerprint != "old-fingerprint" || prompt.NewFingerprint != "new-fingerprint" {
		t.Fatalf("prompt = %+v, want both fingerprints", prompt)
	}
	if prompt.Server != "localhost:64738" {
		t.Fatalf("prompt server = %q, want the normalized address", prompt.Server)
	}

	// Without an explicit acceptance the loop just waits.
	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want 1 while the prompt is unanswered", got)
	}

	m.AcceptFingerprint()

	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	if fp, _ := m.tofu.Fingerprint("localhost"); fp != "new-fingerprint" {
		t.Fatalf("pinned fingerprint = %q, want the accepted one", fp)
	}
}

func TestManagerAcceptFingerprintWithoutPromptIsNoop(t *testing.T) {
	m := newTestManager(t, Callbacks{})

	m.AcceptFingerprint()

	if _, ok := m.tofu.Fingerprint("localhost"); ok {
		t.Fatal("accepting nothing must not pin anything")
	}
}

func TestManagerDisconnectStopsTheLoop(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		attempts.Add(1)
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	m.Disconnect()

	status := sink.expect(t, domain.StateDisconnected)
	if status.Server != "localhost:64738" {
		t.Fatalf("server = %q, want it preserved in the final status", status.Server)
	}
	if got := m.Status().State; got != domain.StateDisconnected {
		t.Fatalf("Status() = %q after Disconnect", got)
	}
}

func TestManagerActionsRequireConnection(t *testing.T) {
	m := newTestManager(t, Callbacks{})

	if err := m.Join(3); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Join without a session = %v, want ErrNotConnected", err)
	}
	if err := m.SendMessage(3, "hi"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("SendMessage without a session = %v, want ErrNotConnected", err)
	}
	if err := m.SendMessage(3, "   "); err == nil {
		t.Fatal("an empty message must be rejected outright")
	}
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		attempts.Add(1)
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	m.Close()
	m.Close()
	after := attempts.Load()

	// Close must also keep a later Connect from resurrecting the manager.
	m.Connect("localhost", "gul", "")
	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != after {
		t.Fatalf("dial attempts = %d after Close, want %d", got, after)
	}
}

func TestChannelToRestore(t *testing.T) {
	root := newChannel(0, "Root", 0)
	target := newChannel(5, "Target", 0)
	addChild(root, target)
	self := &gumble.User{Session: 1, Name: "gul", Channel: root}
	addUser(root, self)

	client := &gumble.Client{
		Self:     self,
		Channels: gumble.Channels{0: root, 5: target},
		Users:    gumble.Users{1: self},
	}

	if got := channelToRestore(client, 5); got != target {
		t.Fatalf("a reachable channel must be restored, got %v", got)
	}

	// Gone while we were away: stay in the root rather than guess.
	if got := channelToRestore(client, 99); got != nil {
		t.Fatalf("a vanished channel must not be restored, got %v", got)
	}

	self.Channel = target
	if got := channelToRestore(client, 5); got != nil {
		t.Fatalf("already in the channel: nothing to move, got %v", got)
	}

	if got := channelToRestore(nil, 5); got != nil {
		t.Fatal("a nil client must not be dereferenced")
	}
	if got := channelToRestore(&gumble.Client{}, 5); got != nil {
		t.Fatal("a client without Self must not be dereferenced")
	}
}

func TestManagerRemembersJoinedChannelAcrossReconnect(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	// What the connect hook of each new session sees as the channel to restore.
	seen := make(chan uint32, 4)
	hooksCh := make(chan sessionHooks, 4)
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		m.mu.Lock()
		channelID, ok := m.restore, m.hasRestore
		m.mu.Unlock()
		if ok {
			seen <- channelID
		} else {
			seen <- 0
		}
		hooksCh <- hooks
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	if got := <-seen; got != 0 {
		t.Fatalf("a fresh Connect must not restore anything, got channel %d", got)
	}

	// Stand in for Join, which needs a live client to move self.
	m.mu.Lock()
	m.restore, m.hasRestore = 5, true
	m.mu.Unlock()

	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError})
	sink.expect(t, domain.StateReconnecting)
	sink.expect(t, domain.StateConnected)

	select {
	case got := <-seen:
		if got != 5 {
			t.Fatalf("channel to restore = %d, want the one joined before the drop", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the second dial")
	}
}

func TestManagerConnectResetsChannelRestore(t *testing.T) {
	m := newTestManager(t, Callbacks{})
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) { return &Session{}, nil }

	m.mu.Lock()
	m.restore, m.hasRestore = 5, true
	m.mu.Unlock()

	// Channel restore is scoped to one Connect, not carried into the next one.
	m.Connect("localhost", "gul", "")

	m.mu.Lock()
	hasRestore := m.hasRestore
	m.mu.Unlock()
	if hasRestore {
		t.Fatal("an explicit Connect must clear the remembered channel")
	}
}
