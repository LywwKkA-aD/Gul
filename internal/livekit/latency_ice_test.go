package livekit

import (
	"net"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// A real ICE/DTLS connection proves the chosen Pion field is populated even
// with no voice traffic. Filtering permits loopback candidates only, with no
// STUN/TURN servers, credentials, hardware devices or external network access.
func TestPeerLatencyReadsRealLoopbackICECheckRTT(t *testing.T) {
	settings := webrtc.SettingEngine{}
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetIncludeLoopbackCandidate(true)
	settings.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settings))
	newPeer := func() *webrtc.PeerConnection {
		pc, err := api.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pc.Close() })
		return pc
	}
	left, right := newPeer(), newPeer()
	if _, err := left.CreateDataChannel("rtt-check", nil); err != nil {
		t.Fatal(err)
	}
	negotiateLoopbackRTT(t, left, right)
	for _, peer := range []*webrtc.PeerConnection{left, right} {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if measured, ok := peerLatency(peer); ok {
				// A loopback request/reply can share one Windows monotonic
				// clock tick. Zero is valid only with an actual ICE response;
				// a positive lower bound tests clock resolution, not this API.
				if !validLatency(measured) {
					t.Fatal("ICE reported an invalid round-trip measurement")
				}
				stats, ok := peer.SCTP().Transport().ICETransport().GetSelectedCandidatePairStats()
				if !ok || stats.ResponsesReceived == 0 || !stats.Nominated || stats.State != webrtc.StatsICECandidatePairStateSucceeded {
					t.Fatal("RTT had no underlying ICE response")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("loopback ICE pair did not yield measured RTT")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := left.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := peerLatency(left); ok {
		t.Fatal("closed connection retained a current RTT")
	}
}

func negotiateLoopbackRTT(t *testing.T, left, right *webrtc.PeerConnection) {
	t.Helper()
	offer, err := left.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	leftGathered := webrtc.GatheringCompletePromise(left)
	if err := left.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	waitICEGathering(t, leftGathered)
	if err := right.SetRemoteDescription(*left.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	answer, err := right.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	rightGathered := webrtc.GatheringCompletePromise(right)
	if err := right.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	waitICEGathering(t, rightGathered)
	if err := left.SetRemoteDescription(*right.LocalDescription()); err != nil {
		t.Fatal(err)
	}
}

func waitICEGathering(t *testing.T, gathered <-chan struct{}) {
	t.Helper()
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		t.Fatal("loopback ICE gathering timed out")
	}
}
