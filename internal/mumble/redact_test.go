package mumble

import (
	"strings"
	"testing"
)

func TestRedactServerRemovesEverySpelling(t *testing.T) {
	cases := []struct {
		name    string
		address string
		text    string
		absent  []string
	}{
		{
			name:    "relay url and the host:port a network error adds",
			address: "wss://murmur.example.test/mumble",
			text: "dial wss://murmur.example.test/mumble: dial tcp 203.0.113.7:443: " +
				"connect: connection refused (murmur.example.test)",
			absent: []string{"murmur.example.test"},
		},
		{
			name:    "direct address",
			address: "murmur.example.test:64738",
			text:    "read tcp 10.0.0.2:51000->murmur.example.test:64738: connection reset",
			absent:  []string{"murmur.example.test"},
		},
		{
			name:    "bare host key of a TOFU mismatch",
			address: "murmur.example.test",
			text:    "server certificate changed since first use: host murmur.example.test pinned aa, got bb",
			absent:  []string{"murmur.example.test"},
		},
		{
			name:    "address the endpoint parser rejects",
			address: "wss://user:hunter2@mumble_server/mumble",
			text:    "invalid WSS server URL: wss://user:hunter2@mumble_server/mumble",
			absent:  []string{"mumble_server", "hunter2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactServer(tc.text, tc.address)
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Fatalf("redacted text still contains %q: %s", absent, got)
				}
			}
			if !strings.Contains(got, redactedServer) {
				t.Fatalf("redacted text does not mark the removal: %s", got)
			}
		})
	}
}

func TestRedactServerKeepsTheRestOfTheMessage(t *testing.T) {
	got := RedactServer("dial murmur.example.test:64738: connect: connection refused",
		"murmur.example.test:64738")

	if !strings.Contains(got, "connection refused") {
		t.Fatalf("redaction ate the diagnosis: %s", got)
	}
}

func TestRedactServerIgnoresEmptyInput(t *testing.T) {
	if got := RedactServer("", "murmur.example.test"); got != "" {
		t.Fatalf("RedactServer(\"\") = %q", got)
	}
	const text = "some error"
	if got := RedactServer(text, ""); got != text {
		t.Fatalf("RedactServer(text, \"\") = %q, want %q", got, text)
	}
}
