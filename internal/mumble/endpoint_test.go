package mumble

import (
	"strings"
	"testing"
)

func TestParseEndpointHysteria(t *testing.T) {
	tests := []struct {
		input   string
		address string
		host    string
		obfs    string
	}{
		{" voice.example.com ", "hysteria2://voice.example.com", "voice.example.com", ""},
		{"voice.example.com:443", "hysteria2://voice.example.com", "voice.example.com", ""},
		{"voice.example.com:8443", "hysteria2://voice.example.com:8443", "voice.example.com", ""},
		{"hy2://voice.example.com", "hysteria2://voice.example.com", "voice.example.com", ""},
		{"HYSTERIA2://VOICE.EXAMPLE.COM.:443/", "hysteria2://voice.example.com", "voice.example.com", ""},
		{"hysteria2://voice.example.com:08443", "hysteria2://voice.example.com:8443", "voice.example.com", ""},
		{"127.0.0.1", "hysteria2://127.0.0.1", "127.0.0.1", ""},
		{"::1", "hysteria2://[::1]", "::1", ""},
		{"[2001:0db8::1]:443", "hysteria2://[2001:db8::1]", "2001:db8::1", ""},
		{"hy2://[2001:db8::1]:8443", "hysteria2://[2001:db8::1]:8443", "2001:db8::1", ""},
		{"hysteria2://voice.example.com?obfs=salamander", "hysteria2://voice.example.com?obfs=salamander", "voice.example.com", "salamander"},
		{"hy2://voice.example.com:443/?obfs=gecko", "hysteria2://voice.example.com?obfs=gecko", "voice.example.com", "gecko"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			ep, err := parseEndpoint(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ep.kind != endpointHysteria || ep.address != tc.address || ep.host != tc.host || ep.obfuscation != tc.obfs {
				t.Fatalf("endpoint = %#v, want address=%q host=%q obfuscation=%q", ep, tc.address, tc.host, tc.obfs)
			}
			second, err := parseEndpoint(ep.address)
			if err != nil || second != ep {
				t.Fatalf("canonical address changed on parsing: %#v, %v", second, err)
			}
		})
	}
}

func TestParseEndpointCanonicalizesEquivalentTOFUHosts(t *testing.T) {
	for _, pair := range [][2]string{
		{"VOICE.EXAMPLE.COM.:443", "hysteria2://voice.example.com"},
		{"hy2://VOICE.EXAMPLE.COM./", "hysteria2://voice.example.com"},
		{"[2001:0DB8::1]:443", "hysteria2://[2001:db8::1]"},
	} {
		first, err := parseEndpoint(pair[0])
		if err != nil {
			t.Fatalf("parse %q: %v", pair[0], err)
		}
		second, err := parseEndpoint(pair[1])
		if err != nil {
			t.Fatalf("parse %q: %v", pair[1], err)
		}
		if first != second {
			t.Fatalf("equivalent endpoints differ: %#v != %#v", first, second)
		}
	}
}

func TestParseEndpointRejectsUnsafeHysteriaURLs(t *testing.T) {
	for _, input := range []string{
		"", " ", "ws://voice.example.com", "https://voice.example.com", "hysteria://voice.example.com",
		"hysteria2:///", "hysteria2://", "hysteria2://user@voice.example.com",
		"hysteria2://user:password@voice.example.com", "hysteria2://voice.example.com/other",
		"hysteria2://voice.example.com/%2f", "hysteria2://voice.example.com#fragment", "hysteria2://voice.example.com#",
		"hysteria2://voice.example.com?", "hysteria2://voice.example.com?auth=password",
		"hysteria2://voice.example.com?target=elsewhere", "hysteria2://voice.example.com?insecure=1",
		"hysteria2://voice.example.com?obfs-password=password", "hysteria2://voice.example.com?obfs=unknown",
		"hysteria2://voice.example.com?obfs=", "hysteria2://voice.example.com?obfs=salamander&obfs=gecko",
		"hysteria2://voice.example.com?obfs=gecko&auth=password", "hysteria2://voice.example.com?obfs=gecko&",
		"hysteria2://voice.example.com?%6fbfs=gecko", "hysteria2://voice.example.com?obfs=%67ecko",
		"voice.example.com?obfs=gecko", "voice.example.com/path", "voice.example.com#fragment",
		"voice.example.com:", "voice.example.com:0", "voice.example.com:65536", "voice.example.com:-1",
		"voice.example.com:abc", "voice.example.com:443:443", "hy2://voice.example.com:",
		"hy2://voice.example.com:0", "hy2://voice.example.com:65536", "hy2://voice.example.com:abc",
		"hy2://voice.example.com..", "hy2://-voice.example.com", "hy2://voice example.com",
		"hy2://[fe80::1%25en0]", "fe80::1%en0", "hy2://[::1]:443\nignored",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseEndpoint(input); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestParseEndpointExplainsRetiredRelayMigration(t *testing.T) {
	_, err := parseEndpoint("wss://voice.example.com/mumble")
	if err == nil || !strings.Contains(err.Error(), "Hysteria") {
		t.Fatalf("error = %v, want migration guidance", err)
	}
}

func TestParseEndpointDoesNotEchoMalformedURLCredentials(t *testing.T) {
	const secret = "do-not-log-this"
	for _, input := range []string{
		"hysteria2://user:" + secret + "@voice.example.com/%zz",
		"hysteria2://voice.example.com?auth=" + secret,
		"hysteria2://voice.example.com?obfs=" + secret,
		"wss://user:" + secret + "@voice.example.com/mumble",
		secret + ":invalid-port",
	} {
		_, err := parseEndpoint(input)
		if err == nil {
			t.Fatal("expected parse error")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("parse error exposed URL credentials")
		}
	}
}
