package mumble

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/relayproto"
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

// drained returns everything recorded so far without blocking. Callers use it
// after the loop has stopped, when no further callback can arrive.
func (s *statusSink) drained() []domain.ConnectionStatus {
	var out []domain.ConnectionStatus
	for {
		select {
		case status := <-s.ch:
			out = append(out, status)
		default:
			return out
		}
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
	// Connect derives the relay bearer before it reports anything, and the
	// real derivation is 600k PBKDF2 rounds - deliberately slow, and slower
	// still under the race detector, which is enough to push a loaded CI
	// runner past the callback deadlines below. The cost itself is covered
	// where it belongs, in wss_test.go against relayproto.Derive; a test that
	// cares about the credential overrides this again.
	m.deriveFn = func([]byte) relayproto.Credential { return "v2.test-credential" }
	// A healthy link by default: tests that care about the round-trip gate say
	// so themselves, and the rest hand out sessions with no client at all.
	m.roundTripFn = func(*gumble.Client) bool { return true }
	t.Cleanup(m.Close)
	return m
}

// A session that never carries a packet of ours back is not a working
// connection, however complete its handshake was. This is the failure a user
// in Russia hit on 2026-08-26: fifty authenticated sessions in a row over a
// link whose outgoing direction was dead, each one looking connected until the
// server gave up on it twenty seconds later.
func TestManagerGivesUpOnASessionThatCarriesNothingBack(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.roundTripGrace = 20 * time.Millisecond
	m.roundTripFn = func(*gumble.Client) bool { return false }

	dials := make(chan struct{}, 4)
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		dials <- struct{}{}
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")

	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)
	// No drop event is ever delivered: the gate is the only thing that can
	// end this session.
	sink.expect(t, domain.StateReconnecting)

	<-dials
	if _, ok := <-dials; !ok {
		t.Fatal("expected a second attempt after the silent session")
	}
}

// The gate must not fire on a link that answers, or every ordinary session
// would be torn down on a timer.
func TestManagerKeepsASessionThatAnswers(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.roundTripGrace = 20 * time.Millisecond
	m.roundTripFn = func(*gumble.Client) bool { return true }
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) { return &Session{}, nil }

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	// Well past the grace: nothing further may be reported.
	time.Sleep(150 * time.Millisecond)
	if extra := sink.drained(); len(extra) != 0 {
		t.Fatalf("a healthy session was disturbed: %+v", extra)
	}
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

func TestManagerRejectsUnsafeRelayURLBeforeDial(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		t.Error("dial must not be attempted for an unsafe relay URL")
		return nil, errors.New("unreachable")
	}

	m.Connect("ws://murmur.example.test/mumble", "gul", "secret")

	status := sink.expect(t, domain.StateDisconnected)
	if status.Error == "" {
		t.Fatal("the rejection must explain itself")
	}
}

func TestManagerPreservesNormalizedRelayURL(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	// Room for every road: a dial that fails for a network reason is retried
	// on the next one straight away (transport.go), so a first connect to a
	// server that is down is more than one attempt.
	configs := make(chan DialConfig, len(relayTransports)+1)
	m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
		configs <- cfg
		return nil, errors.New("stop")
	}

	m.Connect("wss://murmur.example.test", "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	status := sink.expect(t, domain.StateDisconnected)
	if status.Server != "wss://murmur.example.test" {
		t.Fatalf("server = %q", status.Server)
	}
	if cfg := <-configs; cfg.Address != status.Server {
		t.Fatalf("dial address = %q, want %q", cfg.Address, status.Server)
	}
}

func TestRelayAuthenticationErrorsAreTerminal(t *testing.T) {
	for _, err := range []error{ErrRelayPasswordRequired, ErrRelayAuthentication} {
		if !isTerminalDialError(err) {
			t.Errorf("%v was not terminal", err)
		}
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

// testLogger keeps package tests readable: records go to the test log, so a
// failing test still shows what the code reported.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", bytes.TrimRight(p, "\n"))
	return len(p), nil
}

// TestManagerDerivesTheRelayBearerOncePerConnect pins the cost model: PBKDF2
// runs once when the user connects, not once per reconnect attempt.
func TestManagerDerivesTheRelayBearerOncePerConnect(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var derivations atomic.Int32
	m.deriveFn = func([]byte) relayproto.Credential {
		derivations.Add(1)
		return relayproto.Credential("v2.test-credential")
	}

	configs := make(chan DialConfig, 8)
	m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
		configs <- cfg
		// Rate limiting keeps the loop retrying without a live session.
		return nil, &RateLimitedError{RetryAfter: time.Millisecond}
	}

	m.Connect("wss://murmur.example.test", "gul", "server password")
	for range 3 {
		select {
		case cfg := <-configs:
			if cfg.RelayCredential != "v2.test-credential" {
				t.Fatalf("dial credential = %q", cfg.RelayCredential)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for a dial attempt")
		}
	}
	m.Disconnect()

	if got := derivations.Load(); got != 1 {
		t.Fatalf("derivations = %d, want 1 for one Connect", got)
	}
}

func TestManagerWaitsOutTheRelayRetryAfter(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	// The ban has to win over the ordinary ladder, which is 1 ms here.
	const retryAfter = 200 * time.Millisecond

	attempts := make(chan time.Time, 4)
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		attempts <- time.Now()
		return nil, &RateLimitedError{RetryAfter: retryAfter}
	}

	m.Connect("wss://murmur.example.test", "gul", "secret")
	first := <-attempts

	sink.expect(t, domain.StateConnecting)
	status := sink.expect(t, domain.StateConnecting)
	if !strings.Contains(status.Error, "1 секунду") {
		t.Fatalf("status error = %q, want the wait in Russian", status.Error)
	}

	second := <-attempts
	m.Disconnect()
	if waited := second.Sub(first); waited < retryAfter {
		t.Fatalf("waited %s before retrying, want at least %s", waited, retryAfter)
	}
}

// TestManagerRateLimitedFirstConnectStaysOnTheConnectForm is the first-connect
// half of the rate-limit contract. "reconnecting" means a session existed: the
// UI replaces the connect form with the locked main screen and its banner, so
// announcing it for an attempt that never connected leaves the user staring at
// a dimmed session that does not exist, with the form that holds the message
// unmounted.
func TestManagerRateLimitedFirstConnectStaysOnTheConnectForm(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		return nil, &RateLimitedError{RetryAfter: time.Millisecond}
	}

	m.Connect("wss://murmur.example.test", "gul", "secret")
	sink.expect(t, domain.StateConnecting)

	status := sink.expect(t, domain.StateConnecting)
	if !strings.Contains(status.Error, "Следующая попытка") {
		t.Fatalf("status error = %q, want the wait the connect form shows", status.Error)
	}
	if status.Server != "wss://murmur.example.test" {
		t.Fatalf("server = %q, want the normalized address", status.Server)
	}

	// Disconnect waits for the loop, so every status it produced is recorded.
	m.Disconnect()
	for _, recorded := range sink.drained() {
		if recorded.State == domain.StateReconnecting {
			t.Fatal("a first connect entered the reconnect state: there was no session to lose")
		}
	}
}

// TestManagerRateLimitedFirstConnectKeepsLaterFailuresTerminal: waiting out a
// rate limit is not the same as having been connected. The next failure is
// still a first-attempt failure and still belongs on the connect form.
func TestManagerRateLimitedFirstConnectKeepsLaterFailuresTerminal(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		if attempts.Add(1) == 1 {
			return nil, &RateLimitedError{RetryAfter: time.Millisecond}
		}
		return nil, errors.New("connection refused")
	}

	m.Connect("wss://murmur.example.test", "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnecting) // the wait
	sink.expect(t, domain.StateConnecting) // the retry

	status := sink.expect(t, domain.StateDisconnected)
	if status.Error != "connection refused" {
		t.Fatalf("error = %q, want the dial error verbatim", status.Error)
	}

	time.Sleep(50 * time.Millisecond)
	// Three: the rate-limited one, then the refused one, then the other road.
	// A dial that fails with nothing on the other end says something about the
	// road and nothing about the user, so the search happens before the user
	// is sent back to the form (transport.go).
	if got := attempts.Load(); got != 3 {
		t.Fatalf("dial attempts = %d, want 3: the roads are searched first", got)
	}
}

// TestManagerRateLimitedReconnectShowsTheReconnectState is the other half: a
// session did exist, so the locked main screen and its banner are correct -
// and the banner is where the wait has to be readable.
func TestManagerRateLimitedReconnectShowsTheReconnectState(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	hooksCh := make(chan sessionHooks, 4)
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		if attempts.Add(1) == 1 {
			hooksCh <- hooks
			return &Session{}, nil
		}
		return nil, &RateLimitedError{RetryAfter: 20 * time.Millisecond}
	}

	m.Connect("wss://murmur.example.test", "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError, String: "eof"})

	sink.expect(t, domain.StateReconnecting) // the drop
	status := sink.expect(t, domain.StateReconnecting)
	if !strings.Contains(status.Error, "Следующая попытка") {
		t.Fatalf("status error = %q, want the wait visible in the banner", status.Error)
	}
	m.Disconnect()
}

// TestManagerWaitsOutAFullRelay: a relay answering 503 is neither a bad
// password nor a broken address, and the Retry-After it sends is as binding as
// the one a rate limit sends.
func TestManagerWaitsOutAFullRelay(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	const retryAfter = 200 * time.Millisecond

	attempts := make(chan time.Time, 4)
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		attempts <- time.Now()
		return nil, &RelayFullError{RetryAfter: retryAfter}
	}

	m.Connect("wss://murmur.example.test", "gul", "secret")
	first := <-attempts

	sink.expect(t, domain.StateConnecting)
	status := sink.expect(t, domain.StateConnecting)
	if !strings.Contains(status.Error, "переполнен") {
		t.Fatalf("status error = %q, want the Russian capacity message", status.Error)
	}
	if strings.Contains(status.Error, "websocket") {
		t.Fatalf("status error = %q, want no raw transport error", status.Error)
	}

	second := <-attempts
	m.Disconnect()
	if waited := second.Sub(first); waited < retryAfter {
		t.Fatalf("waited %s before retrying, want at least %s", waited, retryAfter)
	}
}

func TestRelayRefusalKeepsTheLadderAsAFloor(t *testing.T) {
	const ladder = 5 * time.Second

	cases := []struct {
		name string
		err  error
		want time.Duration
		says string
	}{
		{
			name: "rate limit shorter than the ladder",
			err:  &RateLimitedError{RetryAfter: time.Second},
			want: ladder,
			says: "отклоняет подключения",
		},
		{
			name: "rate limit longer than the ladder",
			err:  &RateLimitedError{RetryAfter: 30 * time.Second},
			want: 30 * time.Second,
			says: "отклоняет подключения",
		},
		{
			name: "full relay carries its own message",
			err:  &RelayFullError{RetryAfter: 30 * time.Second},
			want: 30 * time.Second,
			says: "переполнен",
		},
		{
			name: "full relay without a Retry-After",
			err:  &RelayFullError{},
			want: ladder,
			says: "переполнен",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wait, message, ok := relayRefusal(tc.err, ladder)
			if !ok {
				t.Fatalf("relayRefusal(%v) did not recognize a transient refusal", tc.err)
			}
			if wait != tc.want {
				t.Fatalf("wait = %s, want %s", wait, tc.want)
			}
			if !strings.Contains(message, tc.says) {
				t.Fatalf("message = %q, want it to contain %q", message, tc.says)
			}
		})
	}

	if _, _, ok := relayRefusal(errors.New("connection refused"), ladder); ok {
		t.Fatal("an ordinary dial failure is not a relay refusal")
	}
}

func TestRateLimitedMessageCountsSeconds(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond: "1 секунду",
		2 * time.Second:        "2 секунды",
		11 * time.Second:       "11 секунд",
		21 * time.Second:       "21 секунду",
		30 * time.Second:       "30 секунд",
	}
	for wait, want := range cases {
		if got := rateLimitedMessage(wait); !strings.Contains(got, want) {
			t.Errorf("rateLimitedMessage(%s) = %q, want it to contain %q", wait, got, want)
		}
	}
}

// TestManagerLogsCarryNoServerAddress is the privacy contract of PLAN.md
// §10.7: diagnostics archives are shareable, so no log record may name the
// server - neither as an attribute nor inside an error string.
func TestManagerLogsCarryNoServerAddress(t *testing.T) {
	const host = "murmur.example.test"
	const address = "wss://" + host + "/mumble"

	var records bytes.Buffer
	m, err := NewManager(t.TempDir(), slog.New(slog.NewJSONHandler(&records, nil)), Callbacks{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
	m.backoffFn = func(int) time.Duration { return time.Millisecond }
	m.deriveFn = func([]byte) relayproto.Credential { return "v2.test-credential" }

	hooksCh := make(chan sessionHooks, 4)
	var attempts atomic.Int32
	m.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		if attempts.Add(1) == 1 {
			hooksCh <- hooks
			return &Session{}, nil
		}
		// What a real failure looks like: the address in our own wrapper and
		// host:port coming from the network layer underneath it.
		return nil, fmt.Errorf("dial %s: dial tcp: lookup %s:443: no such host", address, host)
	}

	m.Connect(address, "gul", "secret")
	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{
		Type:   gumble.DisconnectError,
		String: "read tcp 10.0.0.2:51000->" + host + ":443: connection reset",
	})
	// Give the loop time to log the drop and at least one failed attempt.
	deadline := time.Now().Add(3 * time.Second)
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	m.Disconnect()

	logged := records.String()
	for _, want := range []string{"connect attempt failed", "connection lost"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log is missing the %q record: %s", want, logged)
		}
	}
	if strings.Contains(logged, host) {
		t.Fatalf("log leaked the server address: %s", logged)
	}
	if !strings.Contains(logged, redactedServer) {
		t.Fatalf("log does not show that the address was redacted: %s", logged)
	}
}
