package livekit

import (
	"context"
	"errors"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrokerAddressKeepsExplicitLocalMode(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1:8787", "livekit://127.0.0.1:8787"} {
		got, err := brokerAddress(address)
		if err != nil || got != "http://127.0.0.1:8787" {
			t.Fatalf("%s: %s %v", address, got, err)
		}
	}
	for _, address := range []string{"http://example.com", "http://localhost:8787", "http://127.0.0.1:8787@evil.test", "http://127.0.0.1:8787/?token=secret", "http://127.0.0.1:8787/api", "http://127.0.0.1:8788"} {
		if _, err := brokerAddress(address); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
}

func TestGrantBindsLocalEndpointOwnerRoomAndGeneration(t *testing.T) {
	base := fixtureLogin(0).Grant
	if !validGrant(base, false) {
		t.Fatal("root room rejected")
	}
	cases := []func(*api.Grant){
		func(g *api.Grant) { g.URL = "ws://example.com:7880" },
		func(g *api.Grant) { g.URL = "ws://127.0.0.1:7880?secret=token" },
		func(g *api.Grant) { g.URL = "ws://user:password@127.0.0.1:7880" },
		func(g *api.Grant) { g.URL = "ws://127.0.0.1:7880/redirect" },
		func(g *api.Grant) { g.OwnerIdentity = "voice.8" },
		func(g *api.Grant) { g.Room = "gul-channel-2" },
		func(g *api.Grant) { g.Identity = "voice.0007" },
		func(g *api.Grant) { g.Identity = "voice.2147483648" },
		func(g *api.Grant) { g.Identity = "screen.7" },
		func(g *api.Grant) { g.SessionID = 8 },
		func(g *api.Grant) { g.Token = "" },
		func(g *api.Grant) { g.Revision = 0 },
		func(g *api.Grant) { g.ChannelID = 4 },
	}
	for i, mutate := range cases {
		g := base
		mutate(&g)
		if validGrant(g, false) {
			t.Fatalf("accepted mismatched grant case %d", i)
		}
	}
	base.Identity = "screen.7"
	if !validGrant(base, true) {
		t.Fatal("screen owner binding rejected")
	}
}

func TestBrokerErrorsAndRedirectsCannotExposeCredentials(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authentication")
		}
		http.Redirect(w, r, target.URL+"/?token=secret", http.StatusFound)
	}))
	defer source.Close()
	b := newBroker(source.URL)
	_, err := b.state(context.Background(), "secret")
	if err == nil || strings.Contains(err.Error(), "secret") || reached {
		t.Fatalf("unsafe redirect: %v reached=%v", err, reached)
	}
}

func TestBrokerHonorsRequestCancellation(t *testing.T) {
	b := newBroker("http://127.0.0.1:8787")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.state(ctx, "secret")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
