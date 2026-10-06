//go:build live

package mumble

import (
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// TestPublicHysteriaStaysConnected holds one authenticated Mumble session
// through a deployed Hysteria server for 90 seconds, checking ping responses
// and unexpected reconnects. Run it from each affected network before rollout.
//
// Run only this live test and pass the password through a file descriptor:
//
//	GUL_HYSTERIA_LIVE_PASSWORD_FILE=/dev/stdin \
//	  go test -tags live ./internal/mumble \
//	  -run '^TestPublicHysteriaStaysConnected$' -count=1 -v
func TestPublicHysteriaStaysConnected(t *testing.T) {
	passwordFile := os.Getenv("GUL_HYSTERIA_LIVE_PASSWORD_FILE")
	if passwordFile == "" {
		t.Skip("GUL_HYSTERIA_LIVE_PASSWORD_FILE is not set")
	}
	password := readLivePassword(t, passwordFile)
	defer clear(password)

	address := os.Getenv("GUL_HYSTERIA_LIVE_ADDRESS")
	if address == "" {
		t.Skip("GUL_HYSTERIA_LIVE_ADDRESS is not set")
	}
	username := os.Getenv("GUL_HYSTERIA_LIVE_USERNAME")
	if username == "" {
		username = "gul-hysteria-smoke"
	}

	var mu sync.Mutex
	state := domain.StateDisconnected
	var latency *domain.ConnectionLatency
	updates := make(chan domain.ConnectionStatus, 16)
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), Callbacks{
		OnStatus: func(status domain.ConnectionStatus) {
			mu.Lock()
			state = status.State
			mu.Unlock()
			select {
			case updates <- status:
			default:
			}
		},
		OnLatency: func(sample domain.ConnectionLatency) {
			mu.Lock()
			latency = &sample
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(mgr.Close)

	mgr.Connect(address, username, string(password))
	connectDeadline := time.NewTimer(15 * time.Second)
	defer connectDeadline.Stop()
	for {
		select {
		case status := <-updates:
			if status.State == domain.StateConnected {
				goto connected
			}
			if status.State == domain.StateDisconnected && status.Error != "" {
				t.Fatalf("Hysteria connection failed: %s", RedactServer(status.Error, address))
			}
		case <-connectDeadline.C:
			t.Fatal("Hysteria did not connect within 15 seconds")
		}
	}

connected:
	hold := time.NewTimer(90 * time.Second)
	defer hold.Stop()
	for {
		select {
		case status := <-updates:
			if status.State != domain.StateConnected {
				t.Fatalf("Hysteria session changed state during stability window: %s", status.State)
			}
		case <-hold.C:
			mu.Lock()
			finalState := state
			lastLatency := latency
			mu.Unlock()
			if finalState != domain.StateConnected {
				t.Fatalf("final state = %s, want connected", finalState)
			}
			if lastLatency == nil || math.IsNaN(lastLatency.PingMS) || math.IsInf(lastLatency.PingMS, 0) || lastLatency.PingMS < 0 {
				t.Fatal("no valid TLS/TCP RTT sample arrived during stability window")
			}
			t.Logf("public Hysteria session stayed connected for 90s; RTT %.0f ms", lastLatency.PingMS)
			return
		}
	}
}

func readLivePassword(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read live password: %v", err)
	}
	value = []byte(strings.TrimSuffix(strings.TrimSuffix(string(value), "\n"), "\r"))
	if len(value) == 0 || strings.ContainsAny(string(value), "\r\n") {
		clear(value)
		t.Fatal("live password file must contain exactly one non-empty line")
	}
	return value
}
