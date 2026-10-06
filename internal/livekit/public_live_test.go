//go:build live

package livekit

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

// Public tests require explicit opt-in and private file inputs, never literals
// or credentials on the process command line. Generated tones stay in memory.
// FORCE_RELAY additionally verifies selected TURN/TLS candidates on port 443.
func TestPublicSFUTwoNativeManagers(t *testing.T) {
	if os.Getenv("GUL_LIVEKIT_PUBLIC") != "1" {
		t.Skip("set GUL_LIVEKIT_PUBLIC=1 and ADDRESS_FILE/PASSWORD_FILE inputs")
	}
	address := publicTestInput(t, "GUL_LIVEKIT_ADDRESS_FILE")
	password := publicTestInput(t, "GUL_LIVEKIT_PASSWORD_FILE")
	address, err := brokerAddress(address)
	if err != nil || !strings.HasPrefix(address, "https://") {
		t.Fatal("public fixture requires a valid HTTPS broker origin")
	}
	policy := webrtc.ICETransportPolicyAll
	if os.Getenv("GUL_LIVEKIT_FORCE_RELAY") == "1" {
		policy = webrtc.ICETransportPolicyRelay
	}
	messages := make(chan session.RawMessage, 8)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewManager(logger, session.Callbacks{})
	b := NewManager(logger, session.Callbacks{OnMessage: func(message session.RawMessage) {
		select {
		case messages <- message:
		default:
		}
	}})
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	for _, manager := range []*Manager{a, b} {
		manager.dial = func(ctx context.Context, grant api.Grant, hooks mediaHooks) (mediaConnection, error) {
			return dialMediaWithPolicy(ctx, grant, hooks, policy)
		}
	}
	suffix := time.Now().UnixNano() % 1_000_000
	a.Connect(address, fmt.Sprintf("test-native-a-%d", suffix), password)
	b.Connect(address, fmt.Sprintf("test-native-b-%d", suffix), password)
	liveWait(t, func() bool {
		return a.Status().State == domain.StateConnected && b.Status().State == domain.StateConnected
	}, func() string { return "public native peers did not connect" })
	if policy == webrtc.ICETransportPolicyRelay {
		assertManagerTLSRelay(t, a)
		assertManagerTLSRelay(t, b)
	}
	time.Sleep(250 * time.Millisecond)
	liveVoice(t, a, b, 30)
	liveVoice(t, b, a, 30)
	liveScreenSoundWithPolicy(t, a, b, policy)
	message := fmt.Sprintf("LiveKit native test %d: <plain> & escaped", suffix)
	if err := a.SendMessage(a.Status().SelfChannel, message); err != nil {
		t.Fatal("public reliable chat could not be sent")
	}
	select {
	case got := <-messages:
		if got.HTML != html.EscapeString(message) || got.SenderHash != fmt.Sprintf("s:livekit:%d", a.Status().SelfSession) {
			t.Fatal("public reliable chat payload or sender binding mismatched")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public reliable chat timed out")
	}
	oldEpoch := b.Status().Epoch
	if err := b.Join(2); err != nil {
		t.Fatal("public channel change failed")
	}
	liveWait(t, func() bool {
		return b.Status().State == domain.StateConnected && b.Status().SelfChannel == 2
	}, func() string { return "public peer did not reconnect in another channel" })
	if _, err := b.ScreenGrant(context.Background(), oldEpoch, 1); err == nil {
		t.Fatal("public channel switch retained an old screen grant")
	}
	drainVoice(b)
	liveSend(t, a, 10)
	liveNoVoice(t, b, a.Status().SelfSession, 300*time.Millisecond)
	if err := a.Join(2); err != nil {
		t.Fatal("public sender channel change failed")
	}
	liveWait(t, func() bool {
		return a.Status().State == domain.StateConnected && a.Status().SelfChannel == 2
	}, func() string { return "public sender did not reconnect in another channel" })
	time.Sleep(250 * time.Millisecond)
	liveVoice(t, a, b, 20)
	if policy == webrtc.ICETransportPolicyRelay {
		assertManagerTLSRelay(t, a)
		assertManagerTLSRelay(t, b)
	}
	t.Logf("public native test passed: bidirectional Opus, reliable chat, screen audio, channel isolation; forced_tls_relay=%t", policy == webrtc.ICETransportPolicyRelay)
}

func publicTestInput(t *testing.T, variable string) string {
	t.Helper()
	path := os.Getenv(variable)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("public fixture input file is unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 || strings.TrimSpace(string(data)) == "" {
		t.Fatal("public fixture input file is invalid")
	}
	return strings.TrimSpace(string(data))
}

func assertManagerTLSRelay(t *testing.T, manager *Manager) {
	t.Helper()
	manager.mu.Lock()
	media, ok := manager.media.(*sdkMedia)
	manager.mu.Unlock()
	if !ok {
		t.Fatal("public native media unavailable for relay verification")
	}
	assertTLSRelay(t, media.room)
}

func assertTLSRelay(t *testing.T, room *lksdk.Room) {
	t.Helper()
	for _, pc := range []*webrtc.PeerConnection{
		room.LocalParticipant.GetPublisherPeerConnection(),
		room.LocalParticipant.GetSubscriberPeerConnection(),
	} {
		if pc == nil || pc.SCTP() == nil || pc.SCTP().Transport() == nil {
			t.Fatal("relay verification requires connected WebRTC transports")
		}
		transport := pc.SCTP().Transport().ICETransport()
		pair, err := transport.GetSelectedCandidatePair()
		if err != nil || pair == nil || pair.Local.Typ != webrtc.ICECandidateTypeRelay {
			t.Fatal("forced relay selected a non-relay candidate")
		}
		pairStats, ok := transport.GetSelectedCandidatePairStats()
		if !ok {
			t.Fatal("selected relay candidate statistics unavailable")
		}
		candidate, ok := pc.GetStats()[pairStats.LocalCandidateID].(webrtc.ICECandidateStats)
		if !ok || candidate.CandidateType != webrtc.ICECandidateTypeRelay || candidate.RelayProtocol != "tls" {
			t.Fatal("selected relay did not use TLS")
		}
		turns := 0
		for _, server := range pc.GetConfiguration().ICEServers {
			for _, address := range server.URLs {
				if !strings.HasPrefix(address, "turn") {
					continue
				}
				u, err := url.Parse(address)
				if err != nil || u.Scheme != "turns" {
					t.Fatal("public fixture advertised a non-TLS TURN endpoint")
				}
				_, port, err := net.SplitHostPort(u.Opaque)
				if err != nil || port != "443" || (u.Query().Get("transport") != "" && u.Query().Get("transport") != "tcp") {
					t.Fatal("public fixture TURN/TLS endpoint is not TCP port 443")
				}
				turns++
			}
		}
		if turns == 0 {
			t.Fatal("public fixture advertised no TURN/TLS endpoint")
		}
	}
}
