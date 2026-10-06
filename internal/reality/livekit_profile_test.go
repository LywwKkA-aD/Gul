package reality

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestLiveKitProfileKeepsPasswordSeparateAndPinsHTTPSService(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	raw := "livekit+vless://192.0.2.8:8443?security=reality&flow=none&type=tcp&sni=cover.example&pbk=" + key + "&sid=01ab"
	profile, err := ParseLiveKitProfile(raw)
	if err != nil || profile.Origin != "https://192.0.2.8" || profile.Config.Server != "192.0.2.8:8443" || profile.Config.Password != "" {
		t.Fatal("profile did not separate public HTTPS origin, outer endpoint and credentials")
	}
	if profile.Config.ServerName != "cover.example" || profile.Config.PublicKey != key || profile.Config.ShortID != "01ab" {
		t.Fatal("REALITY settings changed")
	}
	for _, value := range []string{
		strings.Replace(raw, "livekit+vless:", "vless:", 1), raw + "&sid=01ab", raw + "&password=secret",
		strings.Replace(raw, "//192.0.2.8", "//user@192.0.2.8", 1), raw + "#fragment",
		strings.Replace(raw, "?security", "/path?security", 1), strings.Replace(raw, "flow=none", "flow=vision", 1),
		strings.Replace(raw, "sid=01ab", "sid=%30%31ab", 1), strings.Replace(raw, "sni=cover.example", "sni=192.0.2.1", 1),
		strings.Replace(raw, ":8443?", ":0?", 1), strings.Replace(raw, "192.0.2.8", "999.0.0.1", 1),
	} {
		if _, err := ParseLiveKitProfile(value); err == nil {
			t.Fatal("unsafe or ambiguous LiveKit REALITY profile accepted")
		}
	}
	if ipv6, err := ParseLiveKitProfile(strings.Replace(raw, "192.0.2.8", "[2001:db8::8]", 1)); err != nil || ipv6.Origin != "https://[2001:db8::8]" {
		t.Fatal("IPv6 profile rejected")
	}
}
