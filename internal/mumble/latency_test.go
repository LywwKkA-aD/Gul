package mumble

import (
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

func TestSelfStatsChangePublishesLatencyWithoutRebuildingTree(t *testing.T) {
	self := &gumble.User{
		Session: 7,
		Stats: &gumble.UserStats{
			TCPPackets:     2,
			TCPPingAverage: 14.4,
		},
	}
	client := &gumble.Client{Self: self}

	var latencies []domain.ConnectionLatency
	treeUpdates := 0
	m := &Manager{
		client: client,
		cb: Callbacks{
			OnLatency: func(latency domain.ConnectionLatency) {
				latencies = append(latencies, latency)
			},
			OnTree: func(domain.ChannelNode) {
				treeUpdates++
			},
		},
	}

	m.onUserChange(&gumble.UserChangeEvent{
		Client: client,
		User:   self,
		Type:   gumble.UserChangeStats,
	}, "voice.example:64738")

	if len(latencies) != 1 {
		t.Fatalf("latency updates = %d, want 1", len(latencies))
	}
	if diff := math.Abs(latencies[0].PingMS - 14.4); diff > 0.001 {
		t.Fatalf("PingMS = %f, want 14.4", latencies[0].PingMS)
	}
	if treeUpdates != 0 {
		t.Fatalf("tree updates = %d, want 0 for a stats-only event", treeUpdates)
	}
}

func TestInvalidSelfStatsDoNotPublishLatency(t *testing.T) {
	tests := []struct {
		name    string
		packets uint32
		ping    float32
	}{
		{name: "no samples", packets: 0, ping: 1},
		{name: "negative", packets: 1, ping: -1},
		{name: "not a number", packets: 1, ping: float32(math.NaN())},
		{name: "infinite", packets: 1, ping: float32(math.Inf(1))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			self := &gumble.User{Session: 7, Stats: &gumble.UserStats{
				TCPPackets:     tt.packets,
				TCPPingAverage: tt.ping,
			}}
			client := &gumble.Client{Self: self}
			updates := 0
			m := &Manager{
				client: client,
				cb:     Callbacks{OnLatency: func(domain.ConnectionLatency) { updates++ }},
			}

			m.onUserChange(&gumble.UserChangeEvent{
				Client: client,
				User:   self,
				Type:   gumble.UserChangeStats,
			}, "voice.example:64738")

			if updates != 0 {
				t.Fatalf("latency updates = %d, want 0", updates)
			}
		})
	}
}

func TestOtherUserStatsDoNotPublishLatency(t *testing.T) {
	self := &gumble.User{Session: 7}
	other := &gumble.User{Session: 8, Stats: &gumble.UserStats{
		TCPPackets:     2,
		TCPPingAverage: 14.4,
	}}
	client := &gumble.Client{Self: self}
	updates := 0
	m := &Manager{
		client: client,
		cb:     Callbacks{OnLatency: func(domain.ConnectionLatency) { updates++ }},
	}

	m.onUserChange(&gumble.UserChangeEvent{
		Client: client,
		User:   other,
		Type:   gumble.UserChangeStats,
	}, "voice.example:64738")

	if updates != 0 {
		t.Fatalf("latency updates = %d, want 0", updates)
	}
}

func TestOldClientStatsDoNotPublishLatencyAfterSessionReplacement(t *testing.T) {
	oldSelf := &gumble.User{Session: 7, Stats: &gumble.UserStats{
		TCPPackets:     2,
		TCPPingAverage: 14.4,
	}}
	oldClient := &gumble.Client{Self: oldSelf}
	activeClient := &gumble.Client{Self: &gumble.User{Session: 8}}

	updates := 0
	m := &Manager{
		client: activeClient,
		cb: Callbacks{OnLatency: func(domain.ConnectionLatency) {
			updates++
		}},
	}

	m.onUserChange(&gumble.UserChangeEvent{
		Client: oldClient,
		User:   oldSelf,
		Type:   gumble.UserChangeStats,
	}, "voice.example:64738")

	if updates != 0 {
		t.Fatalf("latency updates from replaced client = %d, want 0", updates)
	}
}

func TestStatsWithoutActiveClientDoNotPublishLatency(t *testing.T) {
	self := &gumble.User{Session: 7, Stats: &gumble.UserStats{
		TCPPackets:     2,
		TCPPingAverage: 14.4,
	}}
	client := &gumble.Client{Self: self}

	updates := 0
	m := &Manager{cb: Callbacks{OnLatency: func(domain.ConnectionLatency) { updates++ }}}
	m.onUserChange(&gumble.UserChangeEvent{
		Client: client,
		User:   self,
		Type:   gumble.UserChangeStats,
	}, "voice.example:64738")

	if updates != 0 {
		t.Fatalf("latency updates without an active client = %d, want 0", updates)
	}
}

func TestManagerPollsStatsOnlyWhileConnected(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.statsInterval = 2 * time.Millisecond

	var requests atomic.Int32
	m.requestStatsFn = func(*gumble.Client) { requests.Add(1) }
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		return &Session{}, nil
	}

	m.Connect("localhost", "gul", "")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	deadline := time.Now().Add(time.Second)
	for requests.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := requests.Load(); got < 2 {
		t.Fatalf("stats requests while connected = %d, want at least 2", got)
	}

	m.Disconnect()
	afterDisconnect := requests.Load()
	time.Sleep(10 * time.Millisecond)
	if got := requests.Load(); got != afterDisconnect {
		t.Fatalf("stats requests after Disconnect = %d, want stable %d", got, afterDisconnect)
	}
}
