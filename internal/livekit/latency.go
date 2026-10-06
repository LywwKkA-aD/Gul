package livekit

import (
	"math"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/pion/webrtc/v4"
)

// The SDK uses separate publisher and subscriber connections. Report the
// slower measured selected path, including TURN when ICE chose a relay.
// These are STUN round trips over the media transport, not HTTP request times.
func (c *sdkMedia) latency() (float64, bool) {
	if c.ctx.Err() != nil || c.room == nil {
		return 0, false
	}
	participant := c.room.LocalParticipant
	var slowest float64
	measured := false
	for _, pc := range []*webrtc.PeerConnection{
		participant.GetPublisherPeerConnection(), participant.GetSubscriberPeerConnection(),
	} {
		if pingMS, ok := peerLatency(pc); ok {
			slowest = max(slowest, pingMS)
			measured = true
		}
	}
	return slowest, measured
}

func peerLatency(pc *webrtc.PeerConnection) (float64, bool) {
	if pc == nil || pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		return 0, false
	}
	sctp := pc.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return 0, false
	}
	stats, available := sctp.Transport().ICETransport().GetSelectedCandidatePairStats()
	return candidatePairLatency(stats, available)
}

func candidatePairLatency(stats webrtc.ICECandidatePairStats, available bool) (float64, bool) {
	if !available || stats.State != webrtc.StatsICECandidatePairStateSucceeded || !stats.Nominated || stats.ResponsesReceived == 0 {
		return 0, false
	}
	pingMS := stats.CurrentRoundTripTime * 1000 // Pion reports seconds.
	return pingMS, validLatency(pingMS)
}

func validLatency(pingMS float64) bool {
	return !math.IsNaN(pingMS) && !math.IsInf(pingMS, 0) && pingMS >= 0
}

func (m *Manager) sampleLatency(r *connectionRun, media mediaConnection) {
	if m.cb.OnLatency == nil {
		return
	}
	m.mu.Lock()
	epoch := m.epoch
	active := m.latencySourceCurrent(r, media, epoch)
	m.mu.Unlock()
	if !active {
		return
	}
	pingMS, measured := media.latency()
	if !measured || !validLatency(pingMS) {
		return
	}
	// Collect outside manager locks: Pion owns its own ICE task loop. Fence
	// again under the callback lock so a late sample cannot follow a room
	// change/disconnect event or overwrite the new connection's telemetry.
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	active = m.latencySourceCurrent(r, media, epoch)
	m.mu.Unlock()
	if active {
		m.cb.OnLatency(domain.ConnectionLatency{PingMS: pingMS})
	}
}

// Caller holds m.mu.
func (m *Manager) latencySourceCurrent(r *connectionRun, media mediaConnection, epoch uint64) bool {
	return r != nil && media != nil && m.run == r && r.ctx.Err() == nil &&
		m.media == media && m.epoch == epoch && m.status.State == domain.StateConnected && !m.closed
}
