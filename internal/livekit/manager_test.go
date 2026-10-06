package livekit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
	"github.com/pion/rtp"
)

type fakeBroker struct {
	mu             sync.Mutex
	loginGate      chan struct{}
	screenGate     chan struct{}
	screenStarted  chan struct{}
	audioGate      chan struct{}
	audioStarted   chan struct{}
	channelGate    chan struct{}
	channelStarted chan struct{}
	stateGate      chan struct{}
	stateStarted   chan struct{}
	stateError     error
	current        api.LoginResponse
	loggedOut      int
	closed         int
}

func fixtureLogin(channel uint32) api.LoginResponse {
	return api.LoginResponse{SessionToken: "secret-broker", SessionID: 7, Identity: "voice.7", Name: "alice", ChannelID: channel, Revision: 1, Grant: api.Grant{URL: "ws://127.0.0.1:7880", Token: "secret-jwt", Identity: "voice.7", OwnerIdentity: "voice.7", SessionID: 7, ChannelID: channel, Revision: 1, Room: fmt.Sprintf("gul-channel-%d", channel)}}
}
func (b *fakeBroker) login(ctx context.Context, _, _ string) (api.LoginResponse, error) {
	if b.loginGate != nil {
		select {
		case <-b.loginGate:
		case <-ctx.Done():
			return api.LoginResponse{}, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.current, nil
}
func (b *fakeBroker) state(ctx context.Context, _ string) (api.State, error) {
	if b.stateStarted != nil {
		select {
		case b.stateStarted <- struct{}{}:
		default:
		}
	}
	if b.stateGate != nil {
		select {
		case <-b.stateGate:
		case <-ctx.Done():
			return api.State{}, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return api.State{SelfSession: 7, SelfChannel: b.current.ChannelID, Revision: b.current.Revision}, b.stateError
}
func (b *fakeBroker) channel(ctx context.Context, _ string, id uint32) (api.LoginResponse, error) {
	if b.channelStarted != nil {
		select {
		case b.channelStarted <- struct{}{}:
		default:
		}
	}
	if b.channelGate != nil {
		select {
		case <-b.channelGate:
		case <-ctx.Done():
			return api.LoginResponse{}, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.current.ChannelID = id
	b.current.Revision++
	b.current.Grant.ChannelID = id
	b.current.Grant.Revision = b.current.Revision
	b.current.Grant.Room = fmt.Sprintf("gul-channel-%d", id)
	return b.current, nil
}
func (b *fakeBroker) audio(ctx context.Context, _ string, v api.AudioState) (api.AudioState, error) {
	if b.audioStarted != nil {
		select {
		case b.audioStarted <- struct{}{}:
		default:
		}
	}
	if b.audioGate != nil {
		select {
		case <-b.audioGate:
		case <-ctx.Done():
			return api.AudioState{}, ctx.Err()
		}
	}
	return v, nil
}
func (b *fakeBroker) screen(ctx context.Context, _ string, _ api.ScreenRequest) (api.Grant, error) {
	if b.screenStarted != nil {
		select {
		case b.screenStarted <- struct{}{}:
		default:
		}
	}
	if b.screenGate != nil {
		select {
		case <-b.screenGate:
		case <-ctx.Done():
			return api.Grant{}, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.current.Grant
	g.Identity = "screen.7"
	return g, nil
}
func (b *fakeBroker) logout(context.Context, string) { b.mu.Lock(); b.loggedOut++; b.mu.Unlock() }
func (b *fakeBroker) close()                         { b.mu.Lock(); b.closed++; b.mu.Unlock() }

type fakeMedia struct {
	mu        sync.Mutex
	closed    int
	packets   []*rtp.Packet
	muted     bool
	chatCount int
}

func (f *fakeMedia) write(p *rtp.Packet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packets = append(f.packets, p)
	return nil
}
func (f *fakeMedia) chat(string) error { f.mu.Lock(); f.chatCount++; f.mu.Unlock(); return nil }
func (f *fakeMedia) mute(v bool)       { f.mu.Lock(); f.muted = v; f.mu.Unlock() }
func (f *fakeMedia) close()            { f.mu.Lock(); f.closed++; f.mu.Unlock() }
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}
func testManager(t *testing.T) (*Manager, *fakeBroker, *fakeMedia) {
	t.Helper()
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), session.Callbacks{})
	b := &fakeBroker{current: fixtureLogin(1)}
	f := &fakeMedia{}
	m.brokerFactory = func(string) brokerAPI { return b }
	m.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) { return f, nil }
	t.Cleanup(m.Close)
	return m, b, f
}

func TestDisconnectCancelsPendingLoginWithoutDial(t *testing.T) {
	m, b, _ := testManager(t)
	b.loginGate = make(chan struct{})
	dialed := make(chan struct{}, 1)
	m.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) {
		dialed <- struct{}{}
		return &fakeMedia{}, nil
	}
	m.Connect("http://127.0.0.1:8787", "alice", "secret")
	m.Disconnect()
	close(b.loginGate)
	waitFor(t, func() bool { return m.Status().State == domain.StateDisconnected })
	select {
	case <-dialed:
		t.Fatal("late login created media")
	default:
	}
}

func TestLateMediaCompletionCannotReviveDisconnectedClient(t *testing.T) {
	m, _, f := testManager(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	m.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) {
		close(started)
		<-finish
		return f, nil
	}
	m.Connect("http://127.0.0.1:8787", "alice", "secret")
	<-started
	m.Disconnect()
	close(finish)
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.closed > 0 })
	if m.Status().State != domain.StateDisconnected {
		t.Fatal("late connected state")
	}
}

func TestSelfAudioIntentRemainsUnsettledUntilLatestRequestAcknowledged(t *testing.T) {
	m, b, _ := testManager(t)
	m.Connect("http://127.0.0.1:8787", "alice", "secret")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	b.audioGate = make(chan struct{})
	b.audioStarted = make(chan struct{}, 1)
	m.SetSelfAudio(true, true)
	<-b.audioStarted
	if m.SelfAudioSettled(true, true) {
		t.Fatal("pending request treated as acknowledged")
	}
	m.SetSelfAudio(false, false)
	if m.SelfAudioSettled(false, false) {
		t.Fatal("new intent treated as acknowledged")
	}
	close(b.audioGate)
	waitFor(t, func() bool { return m.SelfAudioSettled(false, false) })
}

func TestScreenGrantRejectsEpochChangedDuringRequest(t *testing.T) {
	m, b, _ := testManager(t)
	m.Connect("http://127.0.0.1:8787", "alice", "secret")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	b.screenGate = make(chan struct{})
	b.screenStarted = make(chan struct{}, 1)
	epoch := m.Status().Epoch
	done := make(chan error, 1)
	go func() { _, err := m.ScreenGrant(context.Background(), epoch, 1); done <- err }()
	<-b.screenStarted
	m.Disconnect()
	close(b.screenGate)
	if err := <-done; !errors.Is(err, ErrStaleSession) {
		t.Fatalf("stale grant: %v", err)
	}
}
