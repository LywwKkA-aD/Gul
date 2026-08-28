package mumble

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

const testRelayAddress = "wss://murmur.example.test"

// A direct Mumble server has one road and nothing to choose between.
func TestChooserOffersOneRoadForADirectServer(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	if roads := c.roads(kindOf(t, "murmur.example.test:64738")); len(roads) != 1 || roads[0] != TransportDirect {
		t.Fatalf("roads = %v, want just the direct one", roads)
	}
	if got := c.next(kindOf(t, "murmur.example.test:64738"), "murmur.example.test:64738"); got != TransportDirect {
		t.Fatalf("next = %q, want %q", got, TransportDirect)
	}
}

// The relay is where the choice lives, and the WebSocket road goes first: it
// is what every deployed relay speaks, so the ordinary case pays nothing.
func TestChooserStartsWithTheWebSocketRoad(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got != TransportWSS {
		t.Fatalf("first road = %q, want %q", got, TransportWSS)
	}
}

// A road that proved itself is kept, so a reconnect does not start the search
// again - and that is the whole point of remembering.
func TestChooserKeepsAProvenRoad(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.failed(testRelayAddress) // the first road did not work
	second := c.next(kindOf(t, testRelayAddress), testRelayAddress)
	if second == TransportWSS {
		t.Fatalf("still on %q after it failed", second)
	}
	c.succeeded(testRelayAddress, second)

	for i := range 5 {
		if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got != second {
			t.Fatalf("attempt %d took %q, want the proven %q", i, got, second)
		}
	}
}

// A remembered road that stops working is forgotten, and the search resumes
// after it rather than retrying what just failed.
func TestChooserForgetsARoadThatStopsWorking(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.succeeded(testRelayAddress, TransportQUIC)
	if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got != TransportQUIC {
		t.Fatalf("next = %q, want the remembered %q", got, TransportQUIC)
	}

	c.failed(testRelayAddress)
	if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got == TransportQUIC {
		t.Fatal("the road that just failed was offered again")
	}
}

// Each server is judged on its own: one blocked network must not decide for
// somebody else's relay.
func TestChooserRemembersPerServer(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.succeeded("wss://one.example.test", TransportQUIC)
	if got := c.next(kindOf(t, "wss://two.example.test"), "wss://two.example.test"); got != TransportWSS {
		t.Fatalf("second server took %q, want the default %q", got, TransportWSS)
	}
}

// The road that carries nothing is abandoned, not retried. This is the failure
// the milestone exists for: connecting proves nothing, so the next attempt has
// to take a different road rather than the same broken one.
func TestManagerTakesAnotherRoadAfterASilentSession(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.roundTripGrace = 20 * time.Millisecond
	m.roundTripFn = func(*gumble.Client) bool { return false }

	roads := make(chan Transport, 8)
	m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
		roads <- cfg.Transport
		return &Session{}, nil
	}

	m.Connect(testRelayAddress, "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)
	reconnecting := sink.expect(t, domain.StateReconnecting)

	// The reconnect banner has to say what happened: an anti-censorship tool
	// that silently retries teaches the user nothing about why nobody heard
	// them. The diagnostic reaches ConnectionStatus.Error, not just the log.
	if reconnecting.Error == "" {
		t.Error("the silent-session reconnect carried no diagnostic for the user")
	}

	first := <-roads
	second := <-roads
	if first == second {
		t.Fatalf("both attempts took %q; the silent road was not abandoned", first)
	}
}

// A dial that fails with nothing on the other end says something about the
// road, so the other one is tried straight away rather than after a wait.
func TestManagerSearchesTheRoadsOnANetworkFailure(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	roads := make(chan Transport, 8)
	m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
		roads <- cfg.Transport
		return nil, errors.New("connection refused")
	}

	m.Connect(testRelayAddress, "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateDisconnected)

	first := <-roads
	select {
	case second := <-roads:
		if first == second {
			t.Fatalf("both attempts took %q", first)
		}
	default:
		t.Fatal("the other road was never tried")
	}
}

// A server that answered - a rejected username, a refused credential - has
// said nothing about the road, and taking another one would only spend the
// user's time twice.
func TestManagerDoesNotSearchTheRoadsWhenTheServerAnswered(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})

	var attempts atomic.Int32
	m.dialFn = func(DialConfig, sessionHooks) (*Session, error) {
		attempts.Add(1)
		// A username already in use is not terminal - the manager retries it -
		// which is exactly why the road check has to look for the answer
		// itself rather than only for terminal failures.
		return nil, &gumble.RejectError{Type: gumble.RejectUsernameInUse}
	}

	m.Connect(testRelayAddress, "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateDisconnected)

	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want exactly 1", got)
	}
}

// A road remembered across launches is a hint, taken before the search.
func TestChooserTakesTheRememberedRoadFirst(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.prefer(kindOf(t, testRelayAddress), testRelayAddress, TransportQUIC)
	if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got != TransportQUIC {
		t.Fatalf("first road = %q, want the remembered %q", got, TransportQUIC)
	}
	// Still only a hint: it has to prove itself, and failing sends the search
	// on as usual.
	c.failed(testRelayAddress)
	if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got == TransportQUIC {
		t.Fatal("the hint survived the road failing")
	}
}

// A settings file is editable, so a road nobody has heard of costs the hint
// and nothing else.
func TestChooserIgnoresARoadItDoesNotKnow(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.prefer(kindOf(t, testRelayAddress), testRelayAddress, Transport("carrier pigeon"))
	if got := c.next(kindOf(t, testRelayAddress), testRelayAddress); got != TransportWSS {
		t.Fatalf("road = %q, want the ordinary first one", got)
	}
	// And a road that exists but not for this address is equally ignored.
	c.prefer(kindOf(t, "murmur.example.test:64738"), "murmur.example.test:64738", TransportQUIC)
	if got := c.next(kindOf(t, "murmur.example.test:64738"), "murmur.example.test:64738"); got != TransportDirect {
		t.Fatalf("direct server took %q", got)
	}
}

// A link that loses both roads and gets one back must reconnect over the one
// that recovered. The road-search budget is spent once per reconnect wave and
// must replenish between waves; without that the chooser froze on whichever
// road it stopped on and never retried the one that came back - permanent
// failure to reconnect in exactly the asymmetric-availability case the roads
// exist for.
func TestManagerRediscoversARoadThatRecovers(t *testing.T) {
	sink := newStatusSink()
	m := newTestManager(t, Callbacks{OnStatus: sink.record})
	m.roundTripGrace = 5 * time.Millisecond
	m.roundTripFn = func(*gumble.Client) bool { return true }

	var mu sync.Mutex
	avail := map[Transport]bool{TransportWSS: true, TransportQUIC: false}
	setAvail := func(wss, quic bool) {
		mu.Lock()
		avail[TransportWSS], avail[TransportQUIC] = wss, quic
		mu.Unlock()
	}

	hooksCh := make(chan sessionHooks, 16)
	m.dialFn = func(cfg DialConfig, hooks sessionHooks) (*Session, error) {
		mu.Lock()
		ok := avail[cfg.Transport]
		mu.Unlock()
		if !ok {
			return nil, errors.New("connection refused")
		}
		hooksCh <- hooks
		return &Session{}, nil
	}

	m.Connect(testRelayAddress, "gul", "secret")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected) // over WSS
	hooks := <-hooksCh
	// Let the round-trip gate mark WSS as the known road, as it would live.
	time.Sleep(15 * time.Millisecond)

	// Both roads vanish, and the live session drops.
	setAvail(false, false)
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError, String: "lost"})
	if _, ok := sink.await(t, domain.StateReconnecting, 2*time.Second); !ok {
		t.Fatal("never entered reconnecting after the drop")
	}
	// Churn through several reconnect waves with both roads down, which is what
	// used to exhaust the one-shot budget and pin the chooser.
	time.Sleep(40 * time.Millisecond)

	// WSS recovers; QUIC stays blocked. A chooser pinned to QUIC never returns.
	setAvail(true, false)
	if _, ok := sink.await(t, domain.StateConnected, 3*time.Second); !ok {
		t.Fatal("never reconnected after the working road recovered")
	}
}

// The road memory has to key a server the way the CALLER spells it, not the
// way Connect normalizes it. Core stores the road beside its own server list,
// keyed by the string the user or the picker supplied - and a server saved by
// an older build is spelled "wss://host/mumble", which normalizes to
// "wss://host". Keyed differently on the two sides, the hint is written under
// one name and looked for under another, and the memory silently never applies:
// the very case it exists for pays the round-trip gate on every launch.
func TestTransportMemoryUsesTheCallersSpelling(t *testing.T) {
	// As an older build stored it. Connect normalizes this to
	// "wss://murmur.example.test", so the two spellings differ.
	const saved = "wss://murmur.example.test/mumble"

	newManager := func(t *testing.T, onTransport func(string, string)) (*Manager, *statusSink, chan Transport) {
		t.Helper()
		sink := newStatusSink()
		m := newTestManager(t, Callbacks{OnStatus: sink.record, OnTransport: onTransport})
		m.roundTripGrace = 10 * time.Millisecond
		m.roundTripFn = func(*gumble.Client) bool { return true }
		roads := make(chan Transport, 4)
		m.dialFn = func(cfg DialConfig, _ sessionHooks) (*Session, error) {
			roads <- cfg.Transport
			return &Session{}, nil
		}
		return m, sink, roads
	}

	// Reading side: a hint seeded with the caller's spelling has to be found
	// when the same server is dialled.
	t.Run("the hint is found", func(t *testing.T) {
		m, sink, roads := newManager(t, nil)
		m.PreferTransport(saved, string(TransportQUIC))

		m.Connect(saved, "gul", "secret")
		sink.expect(t, domain.StateConnecting)
		sink.expect(t, domain.StateConnected)

		if got := <-roads; got != TransportQUIC {
			t.Fatalf("dialled over %q; the remembered road was not used", got)
		}
	})

	// Writing side: what comes back has to carry the caller's spelling, or
	// core cannot match it to the server it belongs to.
	t.Run("the proof carries the same spelling", func(t *testing.T) {
		var mu sync.Mutex
		var reported []string
		m, sink, _ := newManager(t, func(address, _ string) {
			mu.Lock()
			reported = append(reported, address)
			mu.Unlock()
		})

		m.Connect(saved, "gul", "secret")
		sink.expect(t, domain.StateConnecting)
		sink.expect(t, domain.StateConnected)

		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			seen := append([]string(nil), reported...)
			mu.Unlock()
			if len(seen) > 0 {
				if seen[0] != saved {
					t.Fatalf("road reported for %q, want the caller's %q", seen[0], saved)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the proven road was never reported")
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// The same road proving itself on every reconnect must not rewrite the
// settings file each time.
func TestChooserReportsAProvenRoadOnlyWhenItIsNews(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	if !c.succeeded(testRelayAddress, TransportWSS) {
		t.Fatal("the first proof was not news")
	}
	if c.succeeded(testRelayAddress, TransportWSS) {
		t.Fatal("the same road was reported twice")
	}
	if !c.succeeded(testRelayAddress, TransportQUIC) {
		t.Fatal("a different road was not news")
	}
}

// kindOf runs the address through the parser first, which is what the manager
// does before it asks the chooser anything. Passing a raw string straight to
// the chooser is what the bug looked like: for " wss://host" the prefix test
// said direct while the parser said relay, and only dialRelay's silent
// fallback to WebSocket kept it working.
func kindOf(t *testing.T, address string) endpointKind {
	t.Helper()
	ep, err := parseEndpoint(address)
	if err != nil {
		t.Fatalf("parse %q: %v", address, err)
	}
	return ep.kind
}

// A road named but not built has to fail by name.
//
// dialRelay was an else branch: anything that was not QUIC became WebSocket.
// A road added to relayTransports and forgotten here would not have failed -
// it would have lied. The session would run, the round-trip gate would pass,
// and the chooser would record the success under the name of a road that had
// never been dialled, then write that name to the settings file.
func TestARoadOutsideTheTableFailsByName(t *testing.T) {
	t.Parallel()
	ep, err := parseEndpoint(testRelayAddress)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	_, err = dialRelay(t.Context(), ep, Transport("carrier pigeon"), "v2.test", nil, nil, nil)

	if err == nil {
		t.Fatal("an unbuilt road dialled something; it must fail instead")
	}
	if !strings.Contains(err.Error(), "carrier pigeon") {
		t.Fatalf("error = %v, want it to name the road", err)
	}
}

// A caller that names no road gets the default one.
//
// The field's own documentation says empty means the WebSocket road, and the
// else branch used to honour it. The table replaced the else branch and took
// the default with it, so Dial - whose callers choose no road at all - started
// failing with "no road named" against a relay that was answering perfectly.
func TestNamingNoRoadTakesTheFirstOne(t *testing.T) {
	t.Parallel()
	ep, err := parseEndpoint(testRelayAddress)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	_, err = dialRelay(t.Context(), ep, "", "v2.test", NewTOFUStore(t.TempDir(), testLogger(t)), nil, nil)

	// It fails - there is nothing listening - but it must fail having tried to
	// dial, not having refused to choose.
	if err != nil && strings.Contains(err.Error(), "no road named") {
		t.Fatalf("an unnamed road was refused instead of defaulted: %v", err)
	}
}

// Every road the chooser will hand out has to exist in the table, and nothing
// in the table should be unreachable. This is what makes "a road is one entry
// in one table" true rather than intended.
func TestEveryRoadTheChooserOffersIsBuilt(t *testing.T) {
	t.Parallel()
	for _, transport := range relayTransports {
		if _, ok := relayRoads[transport]; !ok {
			t.Errorf("the chooser offers %q and nothing dials it", transport)
		}
	}
	for transport := range relayRoads {
		if !slices.Contains(relayTransports, transport) {
			t.Errorf("%q is built and never offered", transport)
		}
	}
}

// The spelling a person actually pastes.
//
// parseEndpoint trims the address; the chooser used to test the untrimmed
// string for a "wss://" prefix and answer direct. Both halves were wrong in
// opposite directions and cancelled out, because dialRelay dialled WebSocket
// for everything that was not QUIC. Removing that fallback without fixing this
// would have stopped such an address connecting at all.
func TestAPastedAddressIsStillARelayAddress(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	for _, address := range []string{
		testRelayAddress,
		" " + testRelayAddress,
		testRelayAddress + " ",
		"WSS://murmur.example.test",
	} {
		roads := c.roads(kindOf(t, address))
		if len(roads) != len(relayTransports) {
			t.Errorf("roads for %q = %v, want the relay's %v", address, roads, relayTransports)
		}
	}
}
