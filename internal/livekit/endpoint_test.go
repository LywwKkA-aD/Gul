package livekit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
)

func TestRemoteBrokerRequiresCanonicalHTTPSOrigin(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://example.test", "https://example.test"},
		{" https://EXAMPLE.test:443/ ", "https://example.test"},
		{"https://example.test:8443", "https://example.test:8443"},
		{"https://192.0.2.8/", "https://192.0.2.8"},
		{"https://[2001:0db8::8]:443", "https://[2001:db8::8]"},
		{"https://[2001:db8::8]:8443", "https://[2001:db8::8]:8443"},
	} {
		got, err := brokerAddress(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("canonical endpoint mismatch: got=%q error=%v", got, err)
		}
	}
	for _, address := range []string{
		"http://example.test", "wss://example.test", "livekit://example.test", "https:///missing",
		"https://user:secret@example.test", "https://example.test?", "https://example.test?token=secret",
		"https://example.test#secret", "https://example.test/api", "https://example.test/%2f",
		"https://example.test:", "https://example.test:0", "https://example.test:65536",
		"https://0.0.0.0", "https://[::]", "https://224.0.0.1", "https://[fe80::1%25en0]",
		"https://2001:db8::8", "https://999.0.0.1", "https://-bad.test", "https://bad..test",
	} {
		if _, err := brokerAddress(address); err == nil {
			t.Error("unsafe or malformed broker address was accepted")
		}
	}
}

func TestRemoteGrantCannotMoveCredentialsToAnotherAuthority(t *testing.T) {
	for _, tc := range []struct {
		base, media string
		want        bool
	}{
		{"https://example.test", "wss://EXAMPLE.test:443/", true},
		{"https://example.test:8443", "wss://example.test:8443", true},
		{"https://[2001:db8::8]", "wss://[2001:0db8::8]:443", true},
		{"https://192.0.2.8", "wss://192.0.2.8", true},
		{"https://example.test", "ws://example.test", false},
		{"https://example.test", "https://example.test", false},
		{"https://example.test", "wss://elsewhere.test", false},
		{"https://example.test", "wss://example.test:8443", false},
		{"https://example.test", "wss://example.test?token=test", false},
		{"https://example.test", "wss://user:pass@example.test", false},
		{"https://example.test", "wss://example.test/rtc", false},
		{"https://example.test", "ws://127.0.0.1:7880", false},
		{localBrokerAddress, "wss://example.test", false},
		{localBrokerAddress, "ws://127.0.0.1:7880", true},
	} {
		grant := fixtureLogin(1).Grant
		grant.URL = tc.media
		if got := validGrantForBroker(tc.base, grant, false); got != tc.want {
			t.Errorf("authority comparison result=%t want=%t", got, tc.want)
		}
		grant.Identity = "screen.7"
		if got := validGrantForBroker(tc.base, grant, true); got != tc.want {
			t.Errorf("screen authority comparison result=%t want=%t", got, tc.want)
		}
	}
}

func TestManagerRejectsForeignLoginGrantBeforeDial(t *testing.T) {
	m, b, _ := testManager(t)
	b.current.Grant.URL = "wss://elsewhere.test"
	var dialed atomic.Bool
	m.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) {
		dialed.Store(true)
		return &fakeMedia{}, nil
	}
	m.Connect("https://example.test", "alice", "fixture-password")
	waitFor(t, func() bool { return m.Status().Error == ErrBroker.Error() })
	if dialed.Load() {
		t.Fatal("foreign media endpoint received credentials")
	}
}

func TestManagerBindsScreenAndChannelGrantsToHTTPSBroker(t *testing.T) {
	m, b, _ := testManager(t)
	b.current.Grant.URL = "wss://example.test"
	m.Connect("https://EXAMPLE.test:443/", "alice", "fixture-password")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	if m.Status().Server != "https://example.test" {
		t.Fatal("status did not preserve canonical broker authority")
	}
	if _, err := m.ScreenGrant(context.Background(), m.Status().Epoch, 1); err != nil {
		t.Fatal("valid remote screen grant was rejected")
	}
	b.mu.Lock()
	b.current.Grant.URL = "wss://elsewhere.test"
	b.mu.Unlock()
	if _, err := m.ScreenGrant(context.Background(), m.Status().Epoch, 1); !errors.Is(err, ErrBroker) {
		t.Fatal("foreign screen grant was exposed")
	}
	if err := m.Join(2); !errors.Is(err, ErrBroker) {
		t.Fatal("foreign channel grant was accepted")
	}
}
