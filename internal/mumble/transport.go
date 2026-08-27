package mumble

import (
	"slices"
	"strings"
	"sync"
)

// Which road to the relay to take, and how that gets decided.
//
// There are two, and neither is reliably available: a network may drop the
// WebSocket one, or the QUIC one, or neither. The user should not have to know
// which, so the client finds out - and it finds out from the only evidence
// that means anything, which is whether packets of ours come back.
//
// Connecting is not that evidence. Verified live on 2026-08-26: a user
// authenticated fifty times in a row over a road whose outgoing direction was
// dead. So a road is judged by the round-trip gate in the manager, and a road
// that connects and then carries nothing is abandoned exactly like one that
// never connected at all.
type Transport string

const (
	// TransportWSS is the WebSocket tunnel over TCP 443.
	TransportWSS Transport = "wss"
	// TransportQUIC is the scrambled QUIC tunnel over UDP 443.
	TransportQUIC Transport = "quic"
	// TransportDirect is Mumble's own TLS, for a server given as host:port.
	TransportDirect Transport = "direct"
)

// relayTransports is the order the roads are tried in when nothing is known
// about a server yet.
//
// WebSocket first, deliberately: it is what every deployed relay speaks and
// what has carried every session so far, so the common case pays nothing.
//
// The order is not randomised, though an earlier plan said it would be. The
// point of randomising was that a fixed sequence of attempts is itself
// something to recognise - but once a road is remembered there is only ONE
// attempt, so the sequence hardly ever appears. Randomising would buy almost
// nothing and cost a coin flip's worth of extra waiting on every first
// connection.
var relayTransports = []Transport{TransportWSS, TransportQUIC}

// transportChooser picks the road for the next attempt and remembers what
// worked. One per Manager; safe for concurrent use because the reconnect loop
// and the session goroutine both reach it.
//
// The memory lives as long as the process. Carrying it across restarts is
// worth doing - somebody whose WebSocket road is blocked pays the gate's
// twelve seconds once per launch without it - but it needs the choice to reach
// the stored server list, which is a separate change.
type transportChooser struct {
	mu    sync.Mutex
	order []Transport
	index int
	// known is the road that last proved itself, per address.
	known map[string]Transport
}

func newTransportChooser() *transportChooser {
	return &transportChooser{order: relayTransports, known: make(map[string]Transport)}
}

// roads reports the roads that exist for an address. A direct Mumble server
// has exactly one - its own TLS on its own port - and nothing to choose
// between; the relay is where the choice lives.
func (c *transportChooser) roads(address string) []Transport {
	if !strings.HasPrefix(strings.ToLower(address), "wss://") {
		return []Transport{TransportDirect}
	}
	return c.order
}

// next reports the road to try now. A server that has proved a road keeps it
// until that road fails.
func (c *transportChooser) next(address string) Transport {
	roads := c.roads(address)
	if len(roads) == 1 {
		return roads[0]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if known, ok := c.known[address]; ok {
		return known
	}
	return roads[c.index%len(roads)]
}

// succeeded records that this road carried a packet of ours there and back.
// It reports whether that is news, so the same road proving itself on every
// reconnect does not rewrite the settings file each time.
func (c *transportChooser) succeeded(address string, transport Transport) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if known, ok := c.known[address]; ok && known == transport {
		return false
	}
	c.known[address] = transport
	return true
}

// prefer seeds the road to try first, from what was remembered about this
// server across launches. It is a hint, not a verdict: the road still has to
// prove itself, and failing sends the search on as usual.
//
// Unknown roads are ignored rather than refused - a settings file is editable,
// and a road nobody has heard of should cost the hint and nothing else.
func (c *transportChooser) prefer(address string, transport Transport) {
	if !slices.Contains(c.roads(address), transport) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known[address] = transport
}

// failed gives up on the road currently in use and moves to the next one.
// A remembered road that fails is forgotten first, so the next attempt starts
// the search again rather than retrying what just stopped working.
func (c *transportChooser) failed(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if known, ok := c.known[address]; ok {
		delete(c.known, address)
		// Resume the search after the road that just failed, so the next
		// attempt is a different one.
		for i, transport := range c.order {
			if transport == known {
				c.index = i + 1
				return
			}
		}
	}
	c.index++
}
