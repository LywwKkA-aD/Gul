package livekit

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

func TestConnectedManagerPublishesMeasuredMediaLatency(t *testing.T) {
	m, _, media := testManager(t)
	updates := make(chan domain.ConnectionLatency, 8)
	m.cb.OnLatency = func(value domain.ConnectionLatency) {
		// A telemetry callback must not hold the manager state mutex.
		_ = m.Status()
		updates <- value
	}
	m.Connect(localBrokerAddress, "alice", "")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	select {
	case <-updates:
		t.Fatal("latency was fabricated before the first media measurement")
	default:
	}
	media.mu.Lock()
	media.pingMS, media.pingValid = 27.4, true
	media.mu.Unlock()
	select {
	case got := <-updates:
		if got.PingMS != 27.4 {
			t.Fatalf("latency=%v, want measured 27.4ms", got.PingMS)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connected LiveKit manager never published media latency")
	}
}

func TestCandidatePairLatencyRequiresAnActualSelectedPathMeasurement(t *testing.T) {
	valid := webrtc.ICECandidatePairStats{
		State: webrtc.StatsICECandidatePairStateSucceeded, Nominated: true,
		ResponsesReceived: 2, CurrentRoundTripTime: 0.0274,
	}
	for _, tc := range []struct {
		name      string
		available bool
		mutate    func(*webrtc.ICECandidatePairStats)
		want      float64
		measured  bool
	}{
		{"seconds to milliseconds", true, func(*webrtc.ICECandidatePairStats) {}, 27.4, true},
		{"unavailable", false, func(*webrtc.ICECandidatePairStats) {}, 0, false},
		{"before first reply", true, func(s *webrtc.ICECandidatePairStats) { s.ResponsesReceived = 0 }, 0, false},
		{"zero without a reply", true, func(s *webrtc.ICECandidatePairStats) { s.ResponsesReceived, s.CurrentRoundTripTime = 0, 0 }, 0, false},
		{"failed pair", true, func(s *webrtc.ICECandidatePairStats) { s.State = webrtc.StatsICECandidatePairStateFailed }, 0, false},
		{"not nominated", true, func(s *webrtc.ICECandidatePairStats) { s.Nominated = false }, 0, false},
		{"NaN", true, func(s *webrtc.ICECandidatePairStats) { s.CurrentRoundTripTime = math.NaN() }, 0, false},
		{"infinite", true, func(s *webrtc.ICECandidatePairStats) { s.CurrentRoundTripTime = math.Inf(1) }, 0, false},
		{"negative", true, func(s *webrtc.ICECandidatePairStats) { s.CurrentRoundTripTime = -1 }, 0, false},
		{"overflow", true, func(s *webrtc.ICECandidatePairStats) { s.CurrentRoundTripTime = math.MaxFloat64 }, 0, false},
		{"measured local zero", true, func(s *webrtc.ICECandidatePairStats) { s.CurrentRoundTripTime = 0 }, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats := valid
			tc.mutate(&stats)
			got, measured := candidatePairLatency(stats, tc.available)
			if measured != tc.measured || (measured && math.Abs(got-tc.want) > 0.000001) {
				t.Fatalf("latency=%v measured=%t", got, measured)
			}
		})
	}
}

func TestLatencyDoesNotInventMeasurementsForUnconnectedMedia(t *testing.T) {
	if _, ok := peerLatency(nil); ok {
		t.Fatal("nil peer had latency")
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	if _, ok := peerLatency(pc); ok {
		t.Fatal("unconnected peer had latency")
	}
	ctx, cancel := context.WithCancel(context.Background())
	media := &sdkMedia{ctx: ctx}
	if _, ok := media.latency(); ok {
		t.Fatal("uninitialized room had latency")
	}
	media.room = lksdk.NewRoom(lksdk.NewRoomCallback())
	defer media.room.Disconnect()
	if _, ok := media.latency(); ok {
		t.Fatal("room without connected media had latency")
	}
	cancel()
	if _, ok := media.latency(); ok {
		t.Fatal("closed media had latency")
	}
}

type heldLatencyMedia struct {
	fakeMedia
	started chan struct{}
	release chan struct{}
}

func (media *heldLatencyMedia) latency() (float64, bool) {
	close(media.started)
	<-media.release
	return 19.5, true
}

func TestLateLatencyCannotOutliveItsMediaEpoch(t *testing.T) {
	for _, change := range []string{"disconnect", "epoch", "replacement", "reconnecting", "cancel"} {
		t.Run(change, func(t *testing.T) {
			m, _, _ := testManager(t)
			var updates atomic.Int32
			m.cb.OnLatency = func(domain.ConnectionLatency) { updates.Add(1) }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			run := &connectionRun{ctx: ctx, cancel: cancel}
			media := &heldLatencyMedia{started: make(chan struct{}), release: make(chan struct{})}
			m.run, m.media, m.epoch = run, media, 2
			m.status.State = domain.StateConnected
			done := make(chan struct{})
			go func() { m.sampleLatency(run, media); close(done) }()
			<-media.started
			switch change {
			case "disconnect":
				m.Disconnect()
			case "cancel":
				cancel()
			default:
				m.mu.Lock()
				switch change {
				case "epoch":
					m.epoch++
				case "replacement":
					m.media = &fakeMedia{}
				case "reconnecting":
					m.status.State = domain.StateReconnecting
				}
				m.mu.Unlock()
			}
			close(media.release)
			<-done
			if updates.Load() != 0 {
				t.Fatal("obsolete media latency escaped after lifecycle changed")
			}
		})
	}
}

func TestLatencyRejectsInactiveAndMalformedSources(t *testing.T) {
	m, _, media := testManager(t)
	var updates atomic.Int32
	m.cb.OnLatency = func(domain.ConnectionLatency) { updates.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &connectionRun{ctx: ctx, cancel: cancel}
	m.run, m.media, m.epoch = run, media, 2
	m.status.State = domain.StateConnected
	m.sampleLatency(nil, media)
	m.sampleLatency(run, nil)
	m.sampleLatency(run, &fakeMedia{pingMS: 10, pingValid: true})
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		media.pingMS, media.pingValid = invalid, true
		m.sampleLatency(run, media)
	}
	if updates.Load() != 0 {
		t.Fatal("invalid source or measurement was published")
	}
	media.pingMS, media.pingValid = 31.2, true
	m.sampleLatency(run, media)
	if updates.Load() != 1 {
		t.Fatal("active source measurement was not published")
	}
	m.cb.OnLatency = nil
	m.sampleLatency(run, media)
}
