//go:build live

package screenbridge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/livekit"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

// TestBrowserCompanion serves the real production assets and Go bridge for the
// opt-in Playwright companion test. It uses the local SFU by default. All
// capabilities stay in private files; there is no test-control network route.
func TestBrowserCompanion(t *testing.T) {
	if os.Getenv("GUL_BROWSER_COMPANION_LIVE") != "1" {
		t.Skip("opt-in browser companion integration")
	}
	dir := os.Getenv("GUL_BROWSER_COMPANION_DIR")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("a private 0700 test directory is required")
	}
	address, password := "http://127.0.0.1:8787", ""
	if file := os.Getenv("GUL_BROWSER_COMPANION_ADDRESS_FILE"); file != "" {
		address = privateFixture(t, file)
		password = privateFixture(t, os.Getenv("GUL_BROWSER_COMPANION_PASSWORD_FILE"))
	}
	assets, err := filepath.Abs("../../frontend/dist")
	if err != nil {
		t.Fatal("assets unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	owners := make([]*livekit.Manager, 0, 2)
	for _, name := range []string{"a", "b"} {
		manager := livekit.NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), session.Callbacks{})
		t.Cleanup(manager.Close)
		manager.Connect(address, "screen-probe-"+name, password)
		waitConnected(t, ctx, manager)
		bridge := New(manager, os.DirFS(assets), func(url string) error { return writePrivate(filepath.Join(dir, "url-"+name), url+"\n") })
		t.Cleanup(func() { _ = bridge.Close() })
		status := manager.Status()
		if bridge.Open(ctx, status.Epoch, status.SelfChannel) != nil {
			t.Fatal("browser session could not open")
		}
		owners = append(owners, manager)
	}
	control := filepath.Join(dir, "control")
	if writePrivate(control, "") != nil {
		t.Fatal("private control fixture could not be created")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	moved := false
	decoder, err := opus.NewDecoder()
	if err != nil {
		t.Fatal("screen audio decoder unavailable")
	}
	defer decoder.Close()
	pcm := make([]int16, opus.MaxFrameSize)
	owner := owners[0].Status().SelfSession
	audible := 0
	audioReady := false
	for {
		select {
		case packet := <-owners[1].VoicePackets():
			if len(packet.Opus) == 0 || packet.Session != owner|0x80000000 {
				continue
			}
			if packet.Key != fmt.Sprintf("s:livekit:%d", owner) {
				t.Fatal("screen audio lost its native owner mapping")
			}
			n, err := decoder.Decode(packet.Opus, pcm)
			if err != nil {
				t.Fatal("browser screen Opus could not be decoded")
			}
			for _, sample := range pcm[:n] {
				if sample > 500 || sample < -500 {
					audible++
					break
				}
			}
			if audible >= 10 && !audioReady {
				if writePrivate(filepath.Join(dir, "audio-ready"), "decoded-screen-audio\n") != nil {
					t.Fatal("audio result unavailable")
				}
				audioReady = true
			}
		case <-ctx.Done():
			t.Fatal("browser integration did not complete")
		case <-ticker.C:
			data, err := os.ReadFile(control)
			if err != nil {
				t.Fatal("control fixture unavailable")
			}
			switch strings.TrimSpace(string(data)) {
			case "channel":
				if !moved {
					if owners[0].Join(2) != nil {
						t.Fatal("channel move failed")
					}
					moved = true
				}
			case "done":
				if !moved {
					t.Fatal("browser did not verify channel revocation")
				}
				if !audioReady {
					t.Fatal("browser screen audio was not decoded by the native listener")
				}
				return
			case "failed":
				t.Fatal("browser integration reported failure")
			}
		}
	}
}

func privateFixture(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("private fixture permissions required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private fixture unreadable")
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
}

func writePrivate(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

func waitConnected(t *testing.T, ctx context.Context, manager *livekit.Manager) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for manager.Status().State != domain.StateConnected {
		select {
		case <-ctx.Done():
			t.Fatal("native owner failed to connect")
		case <-ticker.C:
		}
	}
}
