package mumble

import (
	"strings"
	"testing"
)

const testRealityKey = "uVJRxRNrt3KSrzfsh39ycJhwm0YeuotDdxxY37WsPlA"
const testRealityQuery = "flow=none&pbk=" + testRealityKey + "&security=reality&sid=1234abcd&sni=cover.example.test&type=tcp"
const testRealityAddress = "vless://voice.example.test?" + testRealityQuery

func TestParseEndpointReality(t *testing.T) {
	for _, input := range []string{
		testRealityAddress,
		" VLESS://VOICE.EXAMPLE.TEST.:0443?type=tcp&sni=COVER.EXAMPLE.TEST.&sid=1234ABCD&security=reality&pbk=" + testRealityKey + "&flow=none ",
	} {
		ep, err := parseEndpoint(input)
		if err != nil {
			t.Fatal(err)
		}
		if ep.kind != endpointReality || ep.address != testRealityAddress || ep.host != "voice.example.test" {
			t.Fatalf("wrong canonical endpoint: %#v", ep)
		}
		if ep.realityServerName != "cover.example.test" || ep.realityPublicKey != testRealityKey || ep.realityShortID != "1234abcd" {
			t.Fatalf("wrong REALITY profile: %#v", ep)
		}
		if again, err := parseEndpoint(ep.address); err != nil || again != ep {
			t.Fatalf("unstable canonical URL: %#v, %v", again, err)
		}
	}
	for _, host := range []string{"[2001:db8::1]:8443", "127.0.0.1:8443"} {
		if _, err := parseEndpoint("vless://" + host + "?" + testRealityQuery); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseEndpointRejectsUnsafeRealityProfiles(t *testing.T) {
	inputs := []string{
		"vless://voice.example.test", "vless://voice.example.test?", "vless://?" + testRealityQuery,
		"vless://user:secret@voice.example.test?" + testRealityQuery,
		"vless://voice.example.test/?" + testRealityQuery,
		"vless://voice.example.test/path?" + testRealityQuery,
		"vless://voice.example.test:0?" + testRealityQuery,
		"vless://voice.example.test:65536?" + testRealityQuery,
		"vless://voice.example.test:?" + testRealityQuery,
		testRealityAddress + "#", testRealityAddress + "#secret", testRealityAddress + "&",
		testRealityAddress + "&target=elsewhere", testRealityAddress + "&password=secret",
		testRealityAddress + "&sid=abcd", testRealityAddress + "&sni=other.test",
		strings.Replace(testRealityAddress, "type=tcp", "type=ws", 1),
		strings.Replace(testRealityAddress, "security=reality", "security=tls", 1),
		strings.Replace(testRealityAddress, "flow=none", "flow=xtls-rprx-vision", 1),
		strings.Replace(testRealityAddress, "sni=cover.example.test", "sni=127.0.0.1", 1),
		strings.Replace(testRealityAddress, "sni=cover.example.test", "sni=bad_host", 1),
		strings.Replace(testRealityAddress, "sid=1234abcd", "sid=123", 1),
		strings.Replace(testRealityAddress, "sid=1234abcd", "sid=", 1),
		strings.Replace(testRealityAddress, "sid=1234abcd", "sid=1234567890abcdef00", 1),
		strings.Replace(testRealityAddress, "sid=1234abcd", "sid=not-hex", 1),
		strings.Replace(testRealityAddress, "pbk="+testRealityKey, "pbk=invalid", 1),
		strings.Replace(testRealityAddress, "pbk="+testRealityKey, "pbk="+testRealityKey+"=", 1),
		strings.Replace(testRealityAddress, "sni=", "%73ni=", 1),
		strings.Replace(testRealityAddress, "sni=cover", "sni=%63over", 1),
		strings.Replace(testRealityAddress, "&type=tcp", "", 1),
	}
	for _, input := range inputs {
		if _, err := parseEndpoint(input); err == nil {
			t.Errorf("accepted unsafe endpoint %q", input)
		}
	}
}

func TestRealityProfileValidationAndRedactionDoNotExposeConfiguration(t *testing.T) {
	const secret = "do-not-log-this"
	for _, input := range []string{
		"vless://user:" + secret + "@voice.example.test/%zz",
		testRealityAddress + "&password=" + secret,
		strings.Replace(testRealityAddress, testRealityKey, secret, 1),
	} {
		_, err := parseEndpoint(input)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe validation error: %v", err)
		}
	}
	text := "dial " + testRealityAddress + " using cover.example.test key " + testRealityKey + " sid 1234abcd"
	redacted := RedactServer(text, testRealityAddress)
	for _, private := range []string{"voice.example.test", "cover.example.test", testRealityKey, "1234abcd"} {
		if strings.Contains(redacted, private) {
			t.Fatalf("profile leaked after redaction: %s", redacted)
		}
	}
	upper := strings.ReplaceAll(strings.ReplaceAll(testRealityAddress, "cover.example.test", "COVER.EXAMPLE.TEST."), "1234abcd", "1234ABCD")
	if got := RedactServer("SNI COVER.EXAMPLE.TEST. short ID 1234ABCD", upper); strings.Contains(got, "EXAMPLE") || strings.Contains(got, "1234ABCD") {
		t.Fatalf("original profile spellings leaked: %s", got)
	}
}

func TestRealityTransportIsExplicitAndIgnoresConflictingHints(t *testing.T) {
	for _, hint := range []Transport{"", "wss", "direct", TransportHysteria, TransportReality} {
		chooser := newTransportChooser()
		chooser.prefer(endpointReality, testRealityAddress, hint)
		if got := chooser.next(endpointReality, testRealityAddress); got != TransportReality {
			t.Fatalf("REALITY endpoint chose %q with hint %q", got, hint)
		}
		chooser.failed(testRealityAddress)
		if got := chooser.next(endpointReality, testRealityAddress); got != TransportReality {
			t.Fatalf("REALITY failed over to %q", got)
		}
		chooser.prefer(endpointHysteria, testRelayAddress, hint)
		if got := chooser.next(endpointHysteria, testRelayAddress); got != TransportHysteria {
			t.Fatalf("Hysteria endpoint chose %q with hint %q", got, hint)
		}
	}
}
