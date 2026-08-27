package config

import (
	"testing"
	"time"
)

const testAddress = "wss://murmur.example.test"

func serverList(t *testing.T) []Server {
	t.Helper()
	return RememberServer(nil, testAddress, "gul", time.Unix(1_700_000_000, 0))
}

// The road that proved itself is what spares somebody whose usual one is
// blocked from paying the round-trip gate on every launch.
func TestRememberTransport(t *testing.T) {
	t.Parallel()
	list := RememberTransport(serverList(t), testAddress, "quic")
	if got := TransportFor(list, testAddress); got != "quic" {
		t.Fatalf("transport = %q, want quic", got)
	}
}

// The road belongs to the server, not to one connect, so connecting again
// must not forget it.
func TestRememberServerKeepsTheRoad(t *testing.T) {
	t.Parallel()
	list := RememberTransport(serverList(t), testAddress, "quic")
	list = RememberServer(list, testAddress, "gul", time.Unix(1_700_000_100, 0))

	if got := TransportFor(list, testAddress); got != "quic" {
		t.Fatalf("transport after reconnect = %q, want quic", got)
	}
}

// A server nobody has connected to gets no road: the hint is only worth
// keeping about a server that is actually in the list.
func TestRememberTransportIgnoresAnUnknownServer(t *testing.T) {
	t.Parallel()
	list := RememberTransport(serverList(t), "wss://elsewhere.example.test", "quic")
	if got := TransportFor(list, "wss://elsewhere.example.test"); got != "" {
		t.Fatalf("transport = %q, want none", got)
	}
	if len(list) != 1 {
		t.Fatalf("list grew to %d entries", len(list))
	}
}

// The settings file is editable, so a road nobody has heard of costs the hint
// and nothing else - the client simply searches, which it does anyway.
func TestUnknownRoadIsDroppedNotHonoured(t *testing.T) {
	t.Parallel()
	if got := RememberTransport(serverList(t), testAddress, "carrier pigeon"); TransportFor(got, testAddress) != "" {
		t.Error("an invented road was stored")
	}

	hand := sanitizeServers([]Server{{
		Address: testAddress, Username: "gul", LastUsed: 1, Transport: "smoke signals",
	}})
	if len(hand) != 1 {
		t.Fatalf("the entry was dropped over its road: %+v", hand)
	}
	if hand[0].Transport != "" {
		t.Errorf("transport = %q, want it cleared", hand[0].Transport)
	}
}
