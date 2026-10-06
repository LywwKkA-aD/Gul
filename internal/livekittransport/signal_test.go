package livekittransport

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/reality"
)

func testGateway(t *testing.T) *Gateway { return testGatewayHost(t, "192.0.2.8") }
func testGatewayHost(t *testing.T, host string) *Gateway {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	p, err := reality.ParseLiveKitProfile("livekit+vless://" + host + "?security=reality&flow=none&type=tcp&sni=cover.example&pbk=" + key + "&sid=01ab")
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(p, "fixture-password", Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	if err := g.BeginEpoch(7); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGatewayBindsCapabilityTokenOriginHostAndEpoch(t *testing.T) {
	g := testGateway(t)
	address, err := g.SignalURL(7, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	request := func() *http.Request {
		r := httptest.NewRequest("GET", strings.Replace(address, "ws:", "http:", 1)+"/rtc?protocol=16", nil)
		r.Header.Set("Authorization", "Bearer fixture-token")
		return r
	}
	if _, _, ok := g.authorize(request()); !ok {
		t.Fatal("native signal denied")
	}
	for _, suffix := range []string{"/rtc/v1", "/rtc/v1/validate"} {
		r := request()
		r.URL.Path = strings.TrimSuffix(r.URL.Path, "/rtc") + suffix
		if _, _, ok := g.authorize(r); !ok {
			t.Fatal("browser v1 signaling denied")
		}
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Host = "evil.example" },
		func(r *http.Request) { r.Header.Set("Origin", "null") },
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer unregistered") },
		func(r *http.Request) { r.URL.Path += "/other" },
		func(r *http.Request) { r.Method = "POST" },
		func(r *http.Request) { r.URL.RawQuery += "&access_token=different" },
	} {
		r := request()
		mutate(r)
		if _, _, ok := g.authorize(r); ok {
			t.Fatal("untrusted signal authorized")
		}
	}
	origin := "http://127.0.0.1:41001"
	if err := g.AllowOrigin(7, origin); err != nil {
		t.Fatal(err)
	}
	r := request()
	r.Header.Set("Origin", origin)
	if _, _, ok := g.authorize(r); !ok {
		t.Fatal("registered companion denied")
	}
	for _, origin := range []string{"null", "http://localhost:41001", "http://127.0.0.1:0", "http://127.0.0.1:41001/path"} {
		if g.AllowOrigin(7, origin) == nil {
			t.Fatal("unsafe origin registered")
		}
	}
	if err := g.BeginEpoch(8); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := g.authorize(request()); ok {
		t.Fatal("old capability survived epoch")
	}
	if _, err := g.SignalURL(7, "fixture-token"); err == nil {
		t.Fatal("stale epoch registered")
	}
	if g.AllowOrigin(7, origin) == nil {
		t.Fatal("stale origin registered")
	}
	g.Close()
	if _, err := g.SignalURL(8, "fixture-token"); err == nil {
		t.Fatal("closed gateway registered token")
	}
}
