//go:build live

package livekit

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/livekittransport"
	"github.com/LywwKkA-aD/Gul/internal/reality"
	"github.com/LywwKkA-aD/Gul/internal/session"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

// Uses the real opt-in Xray + HAProxy + SFU fixture with generated Opus only.
// Its public authority has no directly reachable broker: all requests must
// traverse the outer REALITY endpoint stored in the private profile file.
func TestRealitySFUTwoNativeManagers(t *testing.T) {
	if os.Getenv("GUL_LIVEKIT_REALITY") != "1" {
		t.Skip("requires explicit REALITY fixture opt-in")
	}
	address := publicTestInput(t, "GUL_LIVEKIT_ADDRESS_FILE")
	password := publicTestInput(t, "GUL_LIVEKIT_PASSWORD_FILE")
	if _, err := reality.ParseLiveKitProfile(address); err != nil {
		t.Fatal("invalid REALITY fixture profile")
	}
	var roots *x509.CertPool
	if path := os.Getenv("GUL_LIVEKIT_CA_FILE"); path != "" {
		pem, err := os.ReadFile(path)
		roots = x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			t.Fatal("fixture CA unavailable")
		}
	}
	messages := make(chan session.RawMessage, 8)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	latency := make(chan domain.ConnectionLatency, 8)
	a := NewManager(logger, session.Callbacks{OnLatency: func(sample domain.ConnectionLatency) {
		select {
		case latency <- sample:
		default:
		}
	}})
	b := NewManager(logger, session.Callbacks{OnMessage: func(m session.RawMessage) {
		select {
		case messages <- m:
		default:
		}
	}})
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	for _, m := range []*Manager{a, b} {
		m.gatewayFactory = func(p reality.LiveKitProfile, password string) (*livekittransport.Gateway, error) {
			return livekittransport.New(p, password, livekittransport.Options{RootCAs: roots})
		}
	}
	suffix := time.Now().UnixNano() % 1_000_000
	a.Connect(address, fmt.Sprintf("reality-a-%d", suffix), password)
	b.Connect(address, fmt.Sprintf("reality-b-%d", suffix), password)
	liveWait(t, func() bool {
		return a.Status().State == domain.StateConnected && b.Status().State == domain.StateConnected
	}, func() string {
		return fmt.Sprintf("REALITY peers state=%s/%s error=%s/%s", a.Status().State, b.Status().State, a.Status().Error, b.Status().Error)
	})
	assertManagerGatewayRelay(t, a)
	assertManagerGatewayRelay(t, b)
	for range 2 {
		select {
		case sample := <-latency:
			if !validLatency(sample.PingMS) || sample.PingMS <= 0 {
				t.Fatal("REALITY selected media RTT invalid")
			}
			t.Logf("REALITY selected media RTT: %.2f ms", sample.PingMS)
		case <-time.After(5 * time.Second):
			t.Fatal("REALITY media RTT callback timed out")
		}
	}

	time.Sleep(250 * time.Millisecond)
	liveVoice(t, a, b, 30)
	liveVoice(t, b, a, 30)
	liveScreenSoundWithPolicy(t, a, b, webrtc.ICETransportPolicyRelay)
	if err := a.SendMessage(a.Status().SelfChannel, "REALITY fixture chat"); err != nil {
		t.Fatal("REALITY chat send failed")
	}
	select {
	case got := <-messages:
		if got.HTML != "REALITY fixture chat" {
			t.Fatal("REALITY chat mismatch")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("REALITY chat timed out")
	}
	oldEpoch := b.Status().Epoch
	if err := b.Join(2); err != nil {
		t.Fatal("REALITY channel switch failed")
	}
	liveWait(t, func() bool { return b.Status().State == domain.StateConnected && b.Status().SelfChannel == 2 }, func() string { return "REALITY channel switch timed out" })
	if _, err := b.ScreenGrant(context.Background(), oldEpoch, 1); err == nil {
		t.Fatal("stale REALITY screen grant accepted")
	}
	drainVoice(b)
	liveSend(t, a, 10)
	liveNoVoice(t, b, a.Status().SelfSession, 200*time.Millisecond)
	if err := a.Join(2); err != nil {
		t.Fatal("REALITY sender switch failed")
	}
	liveWait(t, func() bool { return a.Status().State == domain.StateConnected && a.Status().SelfChannel == 2 }, func() string { return "REALITY sender switch timed out" })
	time.Sleep(250 * time.Millisecond)
	liveVoice(t, a, b, 20)
	assertManagerGatewayRelay(t, a)
	assertManagerGatewayRelay(t, b)
	t.Log("REALITY: bidirectional audible Opus, screen audio, chat and channel isolation passed; both WebRTC transports use loopback TURN/TCP only")
}

func assertManagerGatewayRelay(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	media, ok := m.media.(*sdkMedia)
	m.mu.Unlock()
	if !ok {
		t.Fatal("REALITY media unavailable")
	}
	assertGatewayRelay(t, media.room)
}

func assertGatewayRelay(t *testing.T, room *lksdk.Room) {
	t.Helper()
	for _, pc := range []*webrtc.PeerConnection{room.LocalParticipant.GetPublisherPeerConnection(), room.LocalParticipant.GetSubscriberPeerConnection()} {
		if pc == nil || pc.SCTP() == nil || pc.SCTP().Transport() == nil {
			t.Fatal("REALITY WebRTC transport unavailable")
		}
		pair, err := pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil || pair == nil || pair.Local.Typ != webrtc.ICECandidateTypeRelay {
			t.Fatal("REALITY bypassed relay policy")
		}
		config := pc.GetConfiguration()
		if config.ICETransportPolicy != webrtc.ICETransportPolicyRelay || len(config.ICEServers) != 1 || len(config.ICEServers[0].URLs) != 1 {
			t.Fatal("REALITY retained a direct ICE route")
		}
		u, err := url.Parse(config.ICEServers[0].URLs[0])
		if err != nil {
			t.Fatal("REALITY ICE URL invalid")
		}
		host, port, err := net.SplitHostPort(u.Opaque)
		if err != nil || host != "127.0.0.1" || port == "" || u.Scheme != "turn" || !strings.Contains(u.RawQuery, "transport=tcp") {
			t.Fatal("REALITY ICE bypassed its local gateway")
		}
	}
}
