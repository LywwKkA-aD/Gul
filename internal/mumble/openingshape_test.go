package mumble

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// What the classifier of USENIX Security 2024 actually looks at, measured on
// our own wire.
//
// The paper ("encapsulated TLS handshakes", Xue, Kallitsis, Houmansadr,
// Ensafi) takes the first ~25 packets of a flow, quantises sizes into four
// buckets, and builds 3-grams of size-and-direction plus a sequence of bursts,
// where a burst is consecutive packets in one direction with less than 3xRTT
// between them. Its finding is that padding cannot help: padding only grows a
// burst, and multiplexing only adds round trips.
//
// Which is why the cell grid did not settle the question. It gave the wire one
// size, and one size is what our own measurement showed - but the burst
// sequence and the round-trip count are untouched by it, and those are the
// features that carry the signal. Removing the inner handshake is supposed to
// flatten them. The reviewer's objection was that it will not, because the
// burst that remains belongs to Murmur's sync flight rather than to TLS, and
// that taking the client's handshake away leaves that flight standing alone in
// one long unidirectional run - a more legible shape, not a less legible one.
//
// Nobody had measured it. This does.

// burstGap is the "less than 3xRTT" rule at loopback distance. Everything here
// runs on one machine, where an honest RTT is tens of microseconds, so the
// figure is set by what the paper is trying to capture - packets that belong
// to the same flight - rather than by our RTT.
const burstGap = 3 * time.Millisecond

// openingWindow is the paper's observation window: the first packets of the
// flow, and nothing after.
const openingWindow = 25

// wireEvent is one thing an observer on the path sees: which way it went, how
// big it was, and when.
type wireEvent struct {
	at   time.Duration
	up   bool
	size int
}

// wireTrace is the ordered, two-directional record. The existing wire test
// keeps the directions in separate logs, which is enough to ask about sizes
// and useless for asking about bursts: a burst is defined by what came before
// it in the other direction.
type wireTrace struct {
	mu      sync.Mutex
	started time.Time
	events  []wireEvent
	// upgraded is how many events the outer handshake cost. Everything before
	// it is the TLS and HTTP that any WebSocket connection on the web begins
	// with, identical in both scenarios; everything after it is the tunnel,
	// which is the only part the change touches. Guessing the boundary from
	// the shape of the trace was wrong twice - the outer handshake has two
	// upward bursts, not one - so the server marks it instead, from the same
	// process, the moment the upgrade completes.
	upgraded int
}

// seen is how many events have arrived, under the lock the writers use.
func (w *wireTrace) seen() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.events)
}

func (w *wireTrace) mark() {
	w.mu.Lock()
	w.upgraded = len(w.events)
	w.mu.Unlock()
}

// tunnel returns the part of the opening window that belongs to the tunnel.
func (w *wireTrace) tunnel() []wireEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	end := min(openingWindow, len(w.events))
	if w.upgraded > end {
		return nil
	}
	return append([]wireEvent(nil), w.events[w.upgraded:end]...)
}

func (w *wireTrace) add(up bool, size int) {
	w.mu.Lock()
	w.events = append(w.events, wireEvent{at: time.Since(w.started), up: up, size: size})
	w.mu.Unlock()
}

// burst is a run of packets in one direction with no gap long enough to break
// it, which is the unit the classifier compares.
type burst struct {
	up    bool
	count int
	bytes int
}

// bursts groups a trace the way the classifier does.
func bursts(events []wireEvent) []burst {
	var out []burst
	var last time.Duration
	for i, e := range events {
		continues := i > 0 && out[len(out)-1].up == e.up && e.at-last < burstGap
		if !continues {
			out = append(out, burst{up: e.up})
		}
		out[len(out)-1].count++
		out[len(out)-1].bytes += e.size
		last = e.at
	}
	return out
}

// roundTrips counts the direction changes, halved: the paper's "number of
// round trips" in the opening window. Its simplest published rule - fewer than
// 2.5 round trips and a first burst under 300 bytes - keeps 82.5% of ordinary
// connections and 1.5% of proxied ones.
func roundTrips(b []burst) float64 {
	changes := 0
	for i := 1; i < len(b); i++ {
		if b[i].up != b[i-1].up {
			changes++
		}
	}
	return float64(changes) / 2
}

// largestTunnelBurst is the feature the change is meant to remove: a big
// outgoing flight right after a big incoming one, which is what an mTLS
// handshake inside a tunnel looks like and what ordinary browsing does not do.
//
// It is measured over the tunnel's share of the window only. The outer
// handshake before it is Chrome-shaped by utls and identical in both
// scenarios; including it would compare them on the one thing they share and
// hide the thing they do not.
//
// It takes a direction because the classifier reads both, and for a while this
// only looked upward. That blind spot was aimed at exactly the risk this
// milestone ran: removing the nested handshake takes the client's packets out
// of the opening, and what remains is Murmur's sync flight running downward
// with nothing interleaved - a longer one-directional run than before, which
// is the feature n-grams of size-and-direction are built on. A regression that
// grew the downward side could not have failed a test.
func largestTunnelBurst(b []burst, up bool) burst {
	var worst burst
	for _, one := range b {
		if one.up == up && one.bytes > worst.bytes {
			worst = one
		}
	}
	return worst
}

// firstTunnelBurst is the number the paper's simplest published rule reads:
// "fewer than 2.5 round trips AND a first burst after the handshake under 300
// bytes" keeps 82.5% of ordinary connections and 1.5% of proxied ones.
func firstTunnelBurst(b []burst) burst {
	for _, one := range b {
		if one.up {
			return one
		}
	}
	return burst{}
}

func describe(name string, events []wireEvent) string {
	b := bursts(events)
	var out strings.Builder
	fmt.Fprintf(&out, "\n%s\n  %-4s %-9s %-6s %s\n", name, "", "напр.", "пак.", "байт")
	for _, one := range b {
		direction := "вниз"
		if one.up {
			direction = "ВВЕРХ"
		}
		fmt.Fprintf(&out, "       %-9s %-6d %d\n", direction, one.count, one.bytes)
	}
	worst, first := largestTunnelBurst(b, true), firstTunnelBurst(b)
	down := largestTunnelBurst(b, false)
	fmt.Fprintf(&out, "  всплесков: %d   round trip: %.1f\n", len(b), roundTrips(b))
	fmt.Fprintf(&out, "  внутри туннеля: первый клиентский %d пак. / %d байт, крупнейший %d пак. / %d байт\n",
		first.count, first.bytes, worst.count, worst.bytes)
	fmt.Fprintf(&out, "  крупнейший серверный %d пак. / %d байт\n", down.count, down.bytes)
	return out.String()
}

// murmurSyncFlight is what a real server sends a client the moment it has
// authenticated, taken from a live session's own counters: version, ping,
// cryptsetup, codecversion, channelstate, userstate x4, serversync,
// serverconfig, permissionquery - twelve packets, 450 bytes of Mumble in
// total. A quiet room with one channel and four people in it.
//
// The sizes matter more than the names here, and the total is what was
// measured, so the split across the twelve is proportioned rather than
// invented per field. The entries therefore have to add up to 450, and did
// not: they summed to 360 until this was noticed, which made every downward
// reading taken from this fixture a fifth smaller than the session it claims
// to reproduce.
var murmurSyncFlight = []int{31, 25, 50, 25, 56, 44, 44, 44, 44, 31, 38, 18}

// writeSyncFlight sends what Murmur sends a client that has just logged in.
func writeSyncFlight(conn net.Conn) {
	for _, size := range murmurSyncFlight {
		if _, err := conn.Write(mumblePacket(7, make([]byte, size))); err != nil {
			return
		}
	}
}

// traceOpeningShape runs one dial to completion and returns what the wire
// carried, ordered and timed.
//
// serve is the relay's half: it is handed the shaped stream and does whatever
// that scenario does, ending with the sync flight. dial is the client's half.
func traceOpeningShape(
	t *testing.T,
	serve func(*testing.T, net.Conn),
	dial func(*testing.T, endpoint, *http.Client) (net.Conn, error),
	speak func(*testing.T, net.Conn),
) *wireTrace {
	t.Helper()
	const host = "murmur.example.test"
	outerCertificate, outerRoots := testServerCertificate(t, host, 31)
	trace := &wireTrace{started: time.Now()}

	relayServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorizeRelayRequest(w, r) {
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{relayTestNames().Tunnel},
		})
		if err != nil {
			return
		}
		// The upgrade is done; everything from here is the tunnel.
		trace.mark()
		ws.SetReadLimit(relayproto.MaxMessageBytes)
		stream := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
		defer func() { _ = stream.Close() }()
		serve(t, relayproto.Shape(relayproto.AsMessageConn(stream)))
	}))
	relayServer.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{outerCertificate},
	}
	relayServer.StartTLS()
	t.Cleanup(relayServer.Close)

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: outerRoots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			raw, err := (&net.Dialer{}).DialContext(ctx, network, relayServer.Listener.Addr().String())
			if err != nil {
				return nil, err
			}
			trace.started = time.Now()
			return &tracedConn{Conn: raw, trace: trace}, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	port := strings.TrimPrefix(relayServer.URL, "https://127.0.0.1:")
	ep, err := parseEndpoint("wss://" + host + ":" + port)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	conn, err := dial(t, ep, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	speak(t, conn)

	// The opening window is what the classifier reads; give the last of it
	// time to arrive before looking.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if trace.seen() >= openingWindow {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	return trace
}

// tracedConn records both directions of the raw socket into one ordered log,
// which is exactly what somebody sitting on the path has.
type tracedConn struct {
	net.Conn
	trace *wireTrace
}

func (c *tracedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.trace.add(true, n)
	}
	return n, err
}

func (c *tracedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.trace.add(false, n)
	}
	return n, err
}

// TestTheOpeningShapeStaysUnderTheThreshold measures what the classifier
// reads, on the contract we now speak.
//
// The comparison this started as is history: the nested handshake it measured
// against no longer exists in the code. What it measured is recorded, because
// it is the reason the contract changed, and because a number nobody can
// reproduce is a number nobody will believe in six months.
//
//	inside the tunnel, first 25 packets of the flow
//	  with a nested TLS handshake     without it
//	    up   7 packets  2002 bytes      down 1   226
//	    down 4          2256            up   1   286
//	    up   7          2002            down 1   564
//	                                    up   2   572
//	                                    then uniform 286s
//	    round trips 1.0                 round trips 1.5
//	    first client burst 2002 B       first client burst 286 B
//
// The published rule keeps 82.5% of ordinary connections and 1.5% of proxied
// ones: fewer than 2.5 round trips and a first burst under 300 bytes. The old
// contract missed the burst half sevenfold. This test holds the new one to it.
func TestTheOpeningShapeStaysUnderTheThreshold(t *testing.T) {
	t.Parallel()
	const host = "murmur.example.test"
	leaf, _ := testServerCertificate(t, host, 32)

	trace := traceOpeningShape(t,
		func(t *testing.T, shaped net.Conn) {
			if err := serveTestTunnel(shaped, leaf.Certificate[0]); err != nil {
				return
			}
			writeSyncFlight(shaped)
			_, _ = io.Copy(io.Discard, shaped)
		},
		func(t *testing.T, ep endpoint, client *http.Client) (net.Conn, error) {
			return dialWSSTunnel(t.Context(), ep, relayTestCredential(),
				NewTOFUStore(t.TempDir(), testLogger(t)), nil, client)
		},
		func(t *testing.T, conn net.Conn) {
			packets := newPacketConn(conn)
			_, _ = packets.Write(mumblePacket(0, make([]byte, 26)))
			_, _ = packets.Write(mumblePacket(2, make([]byte, 48)))
		})

	inTunnel := bursts(trace.tunnel())
	t.Log(describe("НА ПРОВОДЕ СЕЙЧАС", trace.tunnel()))

	// Measured here rather than the paper's "first burst", because on loopback
	// there is no round trip to separate one from the next: the hello and the
	// login that follows it leave microseconds apart and group into a single
	// burst, which they would not do on any real path - there the hello is
	// answered before the login is written. The number this harness produces
	// for the first burst is therefore an upper bound on the real one, and
	// asserting it would be asserting an artifact of the wire being 200
	// microseconds long.
	//
	// The largest client burst does not depend on that. It was 2002 bytes with
	// the nested handshake, in seven packets; three cells is what the whole
	// opening costs now, hello and login together, and anything above it means
	// something large has come back.
	const clientBurstCeiling = 3 * relayproto.ShapedCellBytes * 2
	worst := largestTunnelBurst(inTunnel, true)
	if worst.bytes >= clientBurstCeiling {
		t.Fatalf("largest client burst in the tunnel = %d bytes in %d packets, want under %d - "+
			"the shape the contract was changed to remove is back", worst.bytes, worst.count, clientBurstCeiling)
	}
	// The downward side is reported by describe() and deliberately not
	// asserted. A ceiling was written here and then removed, because measuring
	// it showed the assertion could only ever have been decoration.
	//
	// Eight identical runs of one build give 2356 bytes six times and 846 the
	// other two. Nothing changed between them. The window holds the first 25
	// events, the contract fills it with chaff cells on its own cadence, and
	// how much of the server's flight lands inside it is a race between that
	// cadence and loopback coalescing the server's writes into one. So any
	// ceiling between those two numbers is a coin flip in CI, and any ceiling
	// above them is passed by the design this milestone rejected: feeding the
	// harness the relay-asks-the-client-to-sign scheme, ~2.2 KB more going
	// down, moved the reading to 846 - smaller, not larger.
	//
	// The upward side does not have the problem, and five runs agree to the
	// byte: the test drives it one write at a time, which is also what a real
	// path does.
	//
	// The downward shape is real and worth guarding. This harness cannot do
	// it, and a flaky guard reads as proof, which is worse than a documented
	// gap. Measuring it needs a path with a real round trip - the pktmon
	// capture that has been asked for and not yet taken.
	const ordinaryRoundTrips = 2.5
	if got := roundTrips(inTunnel); got >= ordinaryRoundTrips {
		t.Fatalf("round trips = %.1f, want under %.1f", got, ordinaryRoundTrips)
	}
}
