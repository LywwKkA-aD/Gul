package relay

import (
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSourceLimitedListenerBoundsOneSourceWithoutStarvingOthers is the
// starvation regression: one source used to be able to hold every
// pre-authentication slot, after which the listener stopped calling Accept and
// the endpoint was offline for everyone.
func TestSourceLimitedListenerBoundsOneSourceWithoutStarvingOthers(t *testing.T) {
	inner := newFakeListener()
	t.Cleanup(func() { _ = inner.Close() })
	listener := LimitListenerBySource(inner, 2, 8, slog.New(slog.DiscardHandler))

	flood := make([]*fakeConn, 4)
	for i := range flood {
		flood[i] = newFakeConn("198.51.100.7")
		inner.offer(flood[i])
	}
	legitimate := newFakeConn("203.0.113.9")
	inner.offer(legitimate)

	for accepted := range 2 {
		conn := acceptWithin(t, listener, time.Second)
		if got := sourcePrefixKey(conn.RemoteAddr()); got != "198.51.100.7" {
			t.Fatalf("accepted %d from %q, want the flooding source", accepted, got)
		}
	}

	// The connections past the per-source limit are dropped inside Accept, and
	// the legitimate source is served without waiting for them.
	conn := acceptWithin(t, listener, time.Second)
	if got := sourcePrefixKey(conn.RemoteAddr()); got != "203.0.113.9" {
		t.Fatalf("accepted from %q, want 203.0.113.9", got)
	}
	for i, rejected := range flood[2:] {
		if !rejected.isClosed() {
			t.Fatalf("over-limit connection %d was not closed", i)
		}
	}
	if got := listener.OpenConnections(); got != 3 {
		t.Fatalf("open connections = %d, want 3", got)
	}
}

func TestSourceLimitedListenerKeysIPv6BySlash64(t *testing.T) {
	inner := newFakeListener()
	t.Cleanup(func() { _ = inner.Close() })
	listener := LimitListenerBySource(inner, 1, 8, slog.New(slog.DiscardHandler))

	first := newFakeConn("2001:db8::1")
	rotated := newFakeConn("2001:db8::dead:beef")
	otherPrefix := newFakeConn("2001:db8:0:1::1")
	inner.offer(first)
	inner.offer(rotated)
	inner.offer(otherPrefix)

	if got := acceptWithin(t, listener, time.Second).RemoteAddr().String(); got != first.RemoteAddr().String() {
		t.Fatalf("first accepted = %s", got)
	}
	if got := acceptWithin(t, listener, time.Second).RemoteAddr().String(); got != otherPrefix.RemoteAddr().String() {
		t.Fatalf("second accepted = %s, want the connection from another /64", got)
	}
	// Rotating inside one /64 is a single subscriber, not a new source.
	if !rotated.isClosed() {
		t.Fatal("address rotation inside the same /64 escaped the limit")
	}
}

func TestSourceLimitedListenerReleasesSlotsOnClose(t *testing.T) {
	inner := newFakeListener()
	t.Cleanup(func() { _ = inner.Close() })
	listener := LimitListenerBySource(inner, 1, 8, slog.New(slog.DiscardHandler))

	inner.offer(newFakeConn("198.51.100.7"))
	first := acceptWithin(t, listener, time.Second)
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Closing twice must not release the slot twice.
	_ = first.Close()
	if got := listener.OpenConnections(); got != 0 {
		t.Fatalf("open connections = %d, want 0", got)
	}

	inner.offer(newFakeConn("198.51.100.7"))
	acceptWithin(t, listener, time.Second)
}

// TestSourceLimitedListenerRejectsPastTheTotalWithoutBlocking keeps the global
// cap from becoming the starvation mechanism it replaced: at the cap the
// listener rejects, it does not suspend accepting.
func TestSourceLimitedListenerRejectsPastTheTotalWithoutBlocking(t *testing.T) {
	inner := newFakeListener()
	t.Cleanup(func() { _ = inner.Close() })
	listener := LimitListenerBySource(inner, 1, 2, slog.New(slog.DiscardHandler))

	inner.offer(newFakeConn("198.51.100.1"))
	inner.offer(newFakeConn("198.51.100.2"))
	first := acceptWithin(t, listener, time.Second)
	acceptWithin(t, listener, time.Second)

	overTotal := newFakeConn("198.51.100.3")
	admitted := newFakeConn("198.51.100.4")
	inner.offer(overTotal)
	pending := acceptAsync(listener)
	waitUntil(t, overTotal.isClosed, "connection past the total limit was not closed")

	// Accept is still running rather than suspended at the cap, so the next
	// connection is served as soon as a slot frees up.
	_ = first.Close()
	inner.offer(admitted)
	select {
	case conn := <-pending:
		if got := conn.RemoteAddr().String(); got != admitted.RemoteAddr().String() {
			t.Fatalf("accepted = %s, want the connection offered after a slot was freed", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener stopped accepting at the total limit")
	}
}

func TestSourceLimitedListenerClosesOverLimitConnectionsOnRealSockets(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener := LimitListenerBySource(inner, 1, 8, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = listener.Close() })

	var accepted atomic.Int64
	held := make(chan net.Conn, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			held <- conn
		}
	}()

	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	var firstServerSide net.Conn
	select {
	case firstServerSide = <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("first connection was not accepted")
	}

	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("over-limit connection stayed open")
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted connections = %d, want 1", got)
	}

	// The listener is still serving: releasing the slot admits the next dial.
	// The slot belongs to the accepted connection, so the server side is the
	// end that has to be closed.
	if err := firstServerSide.Close(); err != nil {
		t.Fatalf("close accepted connection: %v", err)
	}
	third, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("third dial: %v", err)
	}
	t.Cleanup(func() { _ = third.Close() })
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("listener stopped accepting after a rejection")
	}
}

func mustAddr(value string) net.Addr {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		panic(err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		panic("bad address " + value)
	}
	parsedPort := 0
	for _, digit := range port {
		parsedPort = parsedPort*10 + int(digit-'0')
	}
	return &net.TCPAddr{IP: ip, Port: parsedPort}
}

func acceptAsync(listener net.Listener) <-chan net.Conn {
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()
	return accepted
}

func acceptWithin(t *testing.T, listener net.Listener, timeout time.Duration) net.Conn {
	t.Helper()
	select {
	case conn := <-acceptAsync(listener):
		return conn
	case <-time.After(timeout):
		t.Fatal("Accept did not return within the timeout")
		return nil
	}
}

func waitUntil(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type fakeListener struct {
	conns     chan net.Conn
	closeOnce sync.Once
	closed    chan struct{}
}

func newFakeListener() *fakeListener {
	return &fakeListener{conns: make(chan net.Conn, 16), closed: make(chan struct{})}
}

func (l *fakeListener) offer(conn net.Conn) { l.conns <- conn }

func (l *fakeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *fakeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *fakeListener) Addr() net.Addr { return mustAddr("127.0.0.1:0") }

type fakeConn struct {
	net.Conn
	remote net.Addr
	closed atomic.Bool
}

func newFakeConn(ip string) *fakeConn {
	return &fakeConn{remote: &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000}}
}

func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }

func (c *fakeConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *fakeConn) isClosed() bool { return c.closed.Load() }
