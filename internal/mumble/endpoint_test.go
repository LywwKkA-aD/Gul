package mumble

import (
	"strings"
	"testing"
)

func TestParseEndpointDirect(t *testing.T) {
	ep, err := parseEndpoint(" mumble.example.com ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ep.kind != endpointDirect || ep.address != "mumble.example.com:64738" || ep.host != "mumble.example.com" {
		t.Fatalf("endpoint = %#v", ep)
	}
}

func TestParseEndpointWSS(t *testing.T) {
	tests := []struct {
		input   string
		address string
		host    string
	}{
		{"wss://murmur.example.com", "wss://murmur.example.com", "murmur.example.com"},
		{"wss://murmur.example.com", "wss://murmur.example.com", "murmur.example.com"},
		{"wss://murmur.example.com:443", "wss://murmur.example.com:443", "murmur.example.com"},
		{"wss://[2001:db8::1]", "wss://[2001:db8::1]", "2001:db8::1"},
		{"wss://MURMUR.EXAMPLE.COM./mumble", "wss://murmur.example.com", "murmur.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			ep, err := parseEndpoint(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ep.kind != endpointRelay || ep.address != tc.address || ep.host != tc.host {
				t.Fatalf("endpoint = %#v, want address=%q host=%q", ep, tc.address, tc.host)
			}
		})
	}
}

func TestParseEndpointCanonicalizesEquivalentTOFUHosts(t *testing.T) {
	tests := [][2]string{
		{"MURMUR.EXAMPLE.COM.:64738", "murmur.example.com:64738"},
		{"wss://MURMUR.EXAMPLE.COM./mumble", "wss://murmur.example.com"},
	}
	for _, pair := range tests {
		first, err := parseEndpoint(pair[0])
		if err != nil {
			t.Fatalf("parse %q: %v", pair[0], err)
		}
		second, err := parseEndpoint(pair[1])
		if err != nil {
			t.Fatalf("parse %q: %v", pair[1], err)
		}
		if first.host != second.host || first.address != second.address {
			t.Fatalf("equivalent endpoints differ: %#v != %#v", first, second)
		}
	}
}

func TestParseEndpointRejectsUnsafeWSSURLs(t *testing.T) {
	for _, input := range []string{
		"ws://murmur.example.com/mumble",
		"https://murmur.example.com/mumble",
		"wss://user@murmur.example.com/mumble",
		"wss://murmur.example.com/other",
		"wss://murmur.example.com/mumble?target=elsewhere",
		"wss://murmur.example.com/mumble#fragment",
		"wss:///mumble",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseEndpoint(input); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestParseEndpointDoesNotEchoMalformedURLCredentials(t *testing.T) {
	const secret = "do-not-log-this"
	_, err := parseEndpoint("wss://user:" + secret + "@murmur.example.com/%zz")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("parse error exposed URL credentials")
	}
}
