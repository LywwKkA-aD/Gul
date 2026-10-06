package livekit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

func TestTrackBindingHasDeadlineAndHonorsCancellation(t *testing.T) {
	if err := waitBound(context.Background(), func() bool { return false }, time.Millisecond); !errors.Is(err, ErrMedia) {
		t.Fatalf("stalled track: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBound(ctx, func() bool { return false }, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled track: %v", err)
	}
	if err := waitBound(context.Background(), func() bool { return true }, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestChatQueuedDuringChannelChangeDoesNotEnterNewRoom(t *testing.T) {
	m, b, f := testManager(t)
	b.channelGate = make(chan struct{})
	b.channelStarted = make(chan struct{}, 1)
	m.Connect("http://127.0.0.1:8787", "alice", "")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	joined := make(chan error, 1)
	go func() { joined <- m.Join(2) }()
	<-b.channelStarted
	sent := make(chan error, 1)
	go func() { sent <- m.SendMessage(1, "old room message") }()
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.run.commands) == 1 })
	close(b.channelGate)
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	if err := <-sent; !errors.Is(err, ErrStaleSession) {
		t.Fatalf("old message sent: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.chatCount != 0 {
		t.Fatal("old room message was published")
	}
}

func TestOldMediaCallbacksCannotCrossEpochOrResetNewMedia(t *testing.T) {
	m, _, _ := testManager(t)
	hooks := make(chan mediaHooks, 4)
	m.dial = func(_ context.Context, _ api.Grant, h mediaHooks) (mediaConnection, error) {
		hooks <- h
		return &fakeMedia{}, nil
	}
	m.Connect("http://127.0.0.1:8787", "alice", "")
	old := <-hooks
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	old.packet(session.VoicePacket{Session: 9, Opus: []byte{1}})
	if err := m.Join(2); err != nil {
		t.Fatal(err)
	}
	current := <-hooks
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	for len(m.voice.rx) > 0 {
		<-m.voice.rx
	}
	old.packet(session.VoicePacket{Session: 9, Opus: []byte{2}})
	old.reconnect()
	current.packet(session.VoicePacket{Session: 10, Opus: []byte{3}})
	select {
	case p := <-m.voice.rx:
		if p.Session != 10 {
			t.Fatal("old epoch audio escaped")
		}
	case <-time.After(time.Second):
		t.Fatal("current audio missing")
	}
	time.Sleep(20 * time.Millisecond)
	if m.Status().State != domain.StateConnected {
		t.Fatal("old callback disconnected current media")
	}
}

func TestFatalBrokerFailureReleasesQueuedCommandAndRedactsError(t *testing.T) {
	m, b, _ := testManager(t)
	b.stateGate = make(chan struct{})
	b.stateStarted = make(chan struct{}, 1)
	b.stateError = errors.New("request failed with secret-jwt")
	m.Connect("http://127.0.0.1:8787", "alice", "")
	<-b.stateStarted
	sent := make(chan error, 1)
	go func() { sent <- m.SendMessage(1, "queued") }()
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.run.commands) == 1 })
	close(b.stateGate)
	select {
	case err := <-sent:
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("command error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command stuck after terminal broker failure")
	}
	if m.Status().Error != ErrBroker.Error() {
		t.Fatal("broker error detail was exposed")
	}
}

func TestInitialRetryAndChannelChangePreserveVoiceEngineLifecycle(t *testing.T) {
	m, _, _ := testManager(t)
	var mu sync.Mutex
	var states []domain.ConnState
	m.cb.OnStatus = func(s domain.ConnectionStatus) { mu.Lock(); states = append(states, s.State); mu.Unlock() }
	attempts := 0
	m.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) {
		attempts++
		if attempts == 1 {
			return nil, ErrMedia
		}
		return &fakeMedia{}, nil
	}
	m.Connect("http://127.0.0.1:8787", "alice", "")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	mu.Lock()
	for _, s := range states[:len(states)-1] {
		if s == domain.StateReconnecting {
			t.Error("first connection incorrectly claimed an existing voice engine")
		}
	}
	states = nil
	mu.Unlock()
	if err := m.Join(2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	mu.Lock()
	defer mu.Unlock()
	for _, s := range states {
		if s == domain.StateConnecting {
			t.Error("channel change restarts existing voice engine")
		}
	}
}

func TestScreenGrantReturnedAfterRoomSwitchCannotJoinOldRoom(t *testing.T) {
	m, b, _ := testManager(t)
	b.screenGate = make(chan struct{})
	b.screenStarted = make(chan struct{}, 1)
	m.Connect("http://127.0.0.1:8787", "alice", "")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	epoch := m.Status().Epoch
	result := make(chan error, 1)
	go func() { _, err := m.ScreenGrant(context.Background(), epoch, 1); result <- err }()
	<-b.screenStarted
	if err := m.Join(2); err != nil {
		t.Fatal(err)
	}
	close(b.screenGate)
	if err := <-result; !errors.Is(err, ErrStaleSession) {
		t.Fatalf("stale room grant: %v", err)
	}
}
