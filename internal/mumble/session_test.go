package mumble

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/LywwKkA-aD/gumble/gumble"
)

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		in       string
		wantAddr string
		wantHost string
	}{
		{"mumble.example.com", "mumble.example.com:64738", "mumble.example.com"},
		{"mumble.example.com:1234", "mumble.example.com:1234", "mumble.example.com"},
		{"127.0.0.1", "127.0.0.1:64738", "127.0.0.1"},
		{"127.0.0.1:64738", "127.0.0.1:64738", "127.0.0.1"},
		{"::1", "[::1]:64738", "::1"},
		{"[::1]:64738", "[::1]:64738", "::1"},
	}

	for _, tc := range tests {
		addr, host := normalizeAddress(tc.in)
		if addr != tc.wantAddr || host != tc.wantHost {
			t.Errorf("normalizeAddress(%q) = (%q, %q), want (%q, %q)",
				tc.in, addr, host, tc.wantAddr, tc.wantHost)
		}
	}
}

// TestLoggingHooksRedactTheDisconnectReason: the reason a drop carries is
// usually a network error naming the server - by hostname, by resolved IP, or
// both - and gul.log travels inside a shareable diagnostics archive.
func TestLoggingHooksRedactTheDisconnectReason(t *testing.T) {
	const host = "murmur.example.test"

	var records bytes.Buffer
	hooks := loggingHooks(slog.New(slog.NewJSONHandler(&records, nil)), "wss://"+host+"/mumble")

	hooks.disconnect(&gumble.DisconnectEvent{
		Type:   gumble.DisconnectError,
		String: "read tcp 10.0.0.2:51000->203.0.113.7:443: connection reset by " + host,
	})

	logged := records.String()
	for _, secret := range []string{host, "203.0.113.7", "10.0.0.2"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("disconnect record leaked %q: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "connection reset") {
		t.Fatalf("redaction ate the reason: %s", logged)
	}
}
