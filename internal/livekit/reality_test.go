package livekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/livekittransport"
	"github.com/LywwKkA-aD/Gul/internal/reality"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

func realityFixture(t *testing.T) reality.LiveKitProfile {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	p, err := reality.ParseLiveKitProfile("livekit+vless://192.0.2.8:8443?security=reality&flow=none&type=tcp&sni=cover.example&pbk=" + key + "&sid=01ab")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRealityBrokerBindsPublicGrantBeforeLocalSubstitution(t *testing.T) {
	p := realityFixture(t)
	if address, err := brokerAddress(p.Address); err != nil || address != p.Address {
		t.Fatal("public profile rejected")
	}
	g, err := livekittransport.New(p, "fixture-password", livekittransport.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	if err := g.BeginEpoch(9); err != nil {
		t.Fatal(err)
	}
	login := fixtureLogin(1)
	login.Grant.URL = "wss://192.0.2.8"
	if !validLogin(p.Address, login) {
		t.Fatal("matching public grant rejected")
	}
	m := NewManager(nil, session.Callbacks{})
	t.Cleanup(m.Close)
	b := &fakeBroker{current: login}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.run = &connectionRun{ctx: ctx, cancel: cancel, address: p.Address, broker: b, gateway: g}
	m.login, m.epoch = login, 9
	m.status = domain.ConnectionStatus{State: domain.StateConnected, Epoch: 9}
	if err := m.AllowScreenOrigin(9, "http://127.0.0.1:41000"); err != nil {
		t.Fatal(err)
	}
	grant, err := m.ScreenGrant(ctx, 9, 1)
	if err != nil || grant.Transport != "reality" || !grant.RelayOnly || !strings.HasPrefix(grant.URL, "ws://127.0.0.1:") {
		t.Fatal("screen grant did not use authenticated relay gateway")
	}
	b.current.Grant.URL = "wss://other.example"
	if _, err := m.ScreenGrant(ctx, 9, 1); err == nil {
		t.Fatal("foreign grant laundered through loopback")
	}
	m.epoch = 10
	if err := m.AllowScreenOrigin(9, "http://127.0.0.1:41000"); err == nil {
		t.Fatal("stale companion origin accepted")
	}
}
