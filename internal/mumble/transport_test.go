package mumble

import (
	"errors"
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
	if roads := c.roads("murmur.example.test:64738"); len(roads) != 1 || roads[0] != TransportDirect {
		t.Fatalf("roads = %v, want just the direct one", roads)
	}
	if got := c.next("murmur.example.test:64738"); got != TransportDirect {
		t.Fatalf("next = %q, want %q", got, TransportDirect)
	}
}

// The relay is where the choice lives, and the WebSocket road goes first: it
// is what every deployed relay speaks, so the ordinary case pays nothing.
func TestChooserStartsWithTheWebSocketRoad(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	if got := c.next(testRelayAddress); got != TransportWSS {
		t.Fatalf("first road = %q, want %q", got, TransportWSS)
	}
}

// A road that proved itself is kept, so a reconnect does not start the search
// again - and that is the whole point of remembering.
func TestChooserKeepsAProvenRoad(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.failed(testRelayAddress) // the first road did not work
	second := c.next(testRelayAddress)
	if second == TransportWSS {
		t.Fatalf("still on %q after it failed", second)
	}
	c.succeeded(testRelayAddress, second)

	for i := range 5 {
		if got := c.next(testRelayAddress); got != second {
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
	if got := c.next(testRelayAddress); got != TransportQUIC {
		t.Fatalf("next = %q, want the remembered %q", got, TransportQUIC)
	}

	c.failed(testRelayAddress)
	if got := c.next(testRelayAddress); got == TransportQUIC {
		t.Fatal("the road that just failed was offered again")
	}
}

// Each server is judged on its own: one blocked network must not decide for
// somebody else's relay.
func TestChooserRemembersPerServer(t *testing.T) {
	t.Parallel()
	c := newTransportChooser()
	c.succeeded("wss://one.example.test", TransportQUIC)
	if got := c.next("wss://two.example.test"); got != TransportWSS {
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
	sink.expect(t, domain.StateReconnecting)

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
