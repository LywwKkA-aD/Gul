package mumble

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// Stats carry nothing the tree needs, and the latency no longer comes from
// them, so a stats-only event must cost nothing.
func TestSelfStatsChangeDoesNotRebuildTheTree(t *testing.T) {
	self := &gumble.User{Session: 7}
	client := &gumble.Client{Self: self}

	treeUpdates, latencyUpdates := 0, 0
	m := &Manager{
		client: client,
		cb: Callbacks{
			OnTree:    func(domain.ChannelNode) { treeUpdates++ },
			OnLatency: func(domain.ConnectionLatency) { latencyUpdates++ },
		},
	}

	m.onUserChange(&gumble.UserChangeEvent{
		Client: client,
		User:   self,
		Type:   gumble.UserChangeStats,
	}, "voice.example:64738")

	if treeUpdates != 0 {
		t.Fatalf("tree updates = %d, want 0 for a stats-only event", treeUpdates)
	}
	if latencyUpdates != 0 {
		t.Fatalf("latency updates = %d, want 0: stats are not the source", latencyUpdates)
	}
}

// Nothing is published before the first ping has come back: a zero would read
// as a perfect link.
func TestNoLatencyBeforeTheFirstPing(t *testing.T) {
	client := &gumble.Client{Self: &gumble.User{Session: 7}}
	updates := 0
	m := &Manager{client: client, cb: Callbacks{OnLatency: func(domain.ConnectionLatency) { updates++ }}}

	m.sampleLatency(client)

	if updates != 0 {
		t.Fatalf("latency updates = %d, want 0 before any ping", updates)
	}
}

// Listener callbacks can still be queued while a dropped session is replaced.
// Only the active client may speak for the connection.
func TestReplacedClientPublishesNoLatency(t *testing.T) {
	old := &gumble.Client{Self: &gumble.User{Session: 7}}
	active := &gumble.Client{Self: &gumble.User{Session: 8}}

	updates := 0
	m := &Manager{client: active, cb: Callbacks{OnLatency: func(domain.ConnectionLatency) { updates++ }}}

	m.sampleLatency(old)

	if updates != 0 {
		t.Fatalf("latency updates from a replaced client = %d, want 0", updates)
	}
}

func TestLatencyWithoutAnActiveClientIsSilent(t *testing.T) {
	client := &gumble.Client{Self: &gumble.User{Session: 7}}
	updates := 0
	m := &Manager{cb: Callbacks{OnLatency: func(domain.ConnectionLatency) { updates++ }}}

	m.sampleLatency(client)

	if updates != 0 {
		t.Fatalf("latency updates without an active client = %d, want 0", updates)
	}
}

func TestManagerSamplesLatencyOnlyWhileConnected(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.statsInterval = 2 * time.Millisecond

	var samples atomic.Int32
	m.sampleLatencyFn = func(*gumble.Client) { samples.Add(1) }
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	deadline := time.Now().Add(time.Second)
	for samples.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := samples.Load(); got < 2 {
		t.Fatalf("latency samples while connected = %d, want at least 2", got)
	}

	m.Disconnect()
	afterDisconnect := samples.Load()
	time.Sleep(10 * time.Millisecond)
	if got := samples.Load(); got != afterDisconnect {
		t.Fatalf("latency samples after Disconnect = %d, want stable %d", got, afterDisconnect)
	}
}
