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
			// The resolved IP names the server as precisely as the hostname:
			// a diagnostics archive that keeps it still points at the relay.
			absent: []string{"murmur.example.test", "203.0.113.7"},
		},
		{
			name:    "direct address",
			address: "murmur.example.test:64738",
			text:    "read tcp 10.0.0.2:51000->murmur.example.test:64738: connection reset",
			absent:  []string{"murmur.example.test", "10.0.0.2"},
		},
		{
			name:    "IPv6 literal the dialer reports instead of the name",
			address: "wss://murmur.example.test/mumble",
			text:    "dial tcp [2001:db8::7]:443: connect: connection refused",
			absent:  []string{"2001:db8::7"},
		},
		{
			name:    "loopback literal of a local stand",
			address: "wss://murmur.example.test/mumble",
			text:    "dial tcp [::1]:1: connect: connection refused",
			absent:  []string{"[::1]:1"},
		},
		{
			name:    "address the user typed as an IP literal",
			address: "203.0.113.7:64738",
			text:    "dial tcp 203.0.113.7:64738: i/o timeout",
			absent:  []string{"203.0.113.7"},
		},
		{
			name:    "resolved IP inside a value that is not an address",
			address: "wss://murmur.example.test/mumble",
			text:    "handshake failed for map[host:203.0.113.7]",
			absent:  []string{"203.0.113.7"},
		},
		{
			name:    "link-local literal with the interface it is scoped to",
			address: "wss://murmur.example.test/mumble",
			text:    "dial tcp [fe80::1%en0]:443: connect: network is unreachable",
			absent:  []string{"fe80::1"},
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

// TestRedactServerKeepsWhatIsNotAnAddress guards the IP pattern against eating
// the diagnosis it is embedded in: a record that loses its numbers and its
// bracketed values loses its point, and an over-eager pattern is how a
// redactor turns a diagnostics archive into noise.
func TestRedactServerKeepsWhatIsNotAnAddress(t *testing.T) {
	texts := []string{
		"opus encode failed after 3 frames at 48000 Hz, version 1.6.1",
		"unexpected status map[state:connecting attempt:3]",
		"apm stage [aec3:delay] reported 999.999.999.999 ms",
		"frame [0:480] dropped",
	}

	for _, text := range texts {
		if got := RedactServer(text, "murmur.example.test"); got != text {
			t.Fatalf("RedactServer rewrote an address-free record:\n got %s\nwant %s", got, text)
		}
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
	// Without a configured address there is still an address in the text.
	if got := RedactServer("dial tcp 203.0.113.7:443: refused", ""); strings.Contains(got, "203.0.113.7") {
		t.Fatalf("RedactServer kept the resolved IP without an address to compare: %q", got)
	}
}
