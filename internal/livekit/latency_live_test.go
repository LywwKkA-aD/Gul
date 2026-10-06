//go:build live

package livekit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
	"github.com/pion/webrtc/v4"
)

// The RTT smoke peer emits neither chat nor audio and never opens a device.
func TestPublicSFULatency(t *testing.T) {
	if os.Getenv("GUL_LIVEKIT_PUBLIC") != "1" {
		t.Skip("set GUL_LIVEKIT_PUBLIC=1 and ADDRESS_FILE/PASSWORD_FILE inputs")
	}
	address := publicTestInput(t, "GUL_LIVEKIT_ADDRESS_FILE")
	password := publicTestInput(t, "GUL_LIVEKIT_PASSWORD_FILE")
	policy := webrtc.ICETransportPolicyAll
	if os.Getenv("GUL_LIVEKIT_FORCE_RELAY") == "1" {
		policy = webrtc.ICETransportPolicyRelay
	}
	updates := make(chan domain.ConnectionLatency, 16)
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), session.Callbacks{
		OnLatency: func(value domain.ConnectionLatency) {
			select {
			case updates <- value:
			default:
			}
		},
	})
	defer manager.Close()
	manager.dial = func(ctx context.Context, grant api.Grant, hooks mediaHooks) (mediaConnection, error) {
		return dialMediaWithPolicy(ctx, grant, hooks, policy)
	}
	manager.Connect(address, fmt.Sprintf("RTT test %d", time.Now().UnixNano()%1_000_000), password)
	liveWait(t, func() bool { return manager.Status().State == domain.StateConnected }, func() string { return "RTT peer did not connect" })
	if policy == webrtc.ICETransportPolicyRelay {
		assertManagerTLSRelay(t, manager)
	}
	for range 2 {
		select {
		case sample := <-updates:
			if !validLatency(sample.PingMS) || sample.PingMS <= 0 {
				t.Fatal("public ICE measurement was invalid or fabricated")
			}
			t.Logf("measured selected media RTT: %.2f ms; forced_tls_relay=%t", sample.PingMS, policy == webrtc.ICETransportPolicyRelay)
		case <-time.After(10 * time.Second):
			t.Fatal("real selected media RTT never reached OnLatency")
		}
	}
	manager.Disconnect()
	for len(updates) > 0 {
		<-updates
	}
	select {
	case <-updates:
		t.Fatal("latency callback survived disconnect")
	case <-time.After(1100 * time.Millisecond):
	}
}
