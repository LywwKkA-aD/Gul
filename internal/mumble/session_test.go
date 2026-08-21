package mumble

import "testing"

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
