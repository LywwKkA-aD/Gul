package relay

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestProxySessionClosesIdleSessions covers the slot leak: a peer that neither
// reads nor writes used to hold its capacity slot until the process restarted.
func TestProxySessionClosesIdleSessions(t *testing.T) {
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	t.Cleanup(func() {
		_ = clientPeer.Close()
		_ = upstreamPeer.Close()
	})

	finished := make(chan sessionStats, 1)
	go func() {
		finished <- proxySession(client, upstream, 100*time.Millisecond, 5*time.Second)
	}()

	select {
	case stats := <-finished:
		if stats.reason != "idle timeout" {
			t.Fatalf("close reason = %q, want idle timeout", stats.reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle session was never closed")
	}
}

func TestProxySessionKeepsTransferringSessionsOpen(t *testing.T) {
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	t.Cleanup(func() {
		_ = clientPeer.Close()
		_ = upstreamPeer.Close()
	})

	finished := make(chan sessionStats, 1)
	go func() {
		finished <- proxySession(client, upstream, 150*time.Millisecond, 5*time.Second)
	}()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, upstreamPeer)
	}()

	// Six pings across four idle windows: a live Mumble session behaves the
	// same way and must never be closed by the watchdog.
	for range 6 {
		if _, err := clientPeer.Write([]byte{0x20}); err != nil {
			t.Fatalf("write ping: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case stats := <-finished:
		t.Fatalf("active session was closed: %q", stats.reason)
	default:
	}

	_ = clientPeer.Close()
	select {
	case stats := <-finished:
		if stats.reason != "client closed" {
			t.Fatalf("close reason = %q, want client closed", stats.reason)
		}
		if stats.fromClient != 6 {
			t.Fatalf("bytes from client = %d, want 6", stats.fromClient)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end after the client closed")
	}
	<-drained
}

// TestProxySessionEnforcesWriteDeadline covers the other half of the leak: a
// peer that stops reading stalls the opposite direction, which without a
// deadline blocks the copy goroutine forever.
func TestProxySessionEnforcesWriteDeadline(t *testing.T) {
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	t.Cleanup(func() {
		_ = clientPeer.Close()
		_ = upstreamPeer.Close()
	})

	finished := make(chan sessionStats, 1)
	go func() {
		finished <- proxySession(client, upstream, time.Minute, 100*time.Millisecond)
	}()

	// The client never reads, so this byte can be handed to the relay but
	// never delivered.
	if _, err := upstreamPeer.Write([]byte{0x01}); err != nil {
		t.Fatalf("write from upstream: %v", err)
	}

	select {
	case stats := <-finished:
		if stats.reason != "write timeout" {
			t.Fatalf("close reason = %q, want write timeout", stats.reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled write was never abandoned")
	}
}

func TestProxySessionCountsBytesInEachDirection(t *testing.T) {
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	t.Cleanup(func() { _ = upstreamPeer.Close() })

	finished := make(chan sessionStats, 1)
	go func() {
		finished <- proxySession(client, upstream, time.Minute, 5*time.Second)
	}()
	go func() {
		buffer := make([]byte, 8)
		read, err := upstreamPeer.Read(buffer)
		if err != nil {
			return
		}
		_, _ = upstreamPeer.Write(buffer[:read])
		_, _ = upstreamPeer.Write([]byte{0xff})
	}()

	if _, err := clientPeer.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("write: %v", err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(clientPeer, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	_ = clientPeer.Close()

	select {
	case stats := <-finished:
		if stats.fromClient != 4 {
			t.Fatalf("bytes from client = %d, want 4", stats.fromClient)
		}
		if stats.toClient != 5 {
			t.Fatalf("bytes to client = %d, want 5", stats.toClient)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish")
	}
}

// TestHandlerReleasesCapacityWhenASessionGoesIdle is the end-to-end form of the
// leak: with one slot in total, an idle session must not keep the next client
// out forever.
func TestHandlerReleasesCapacityWhenASessionGoesIdle(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.Upstream = echoServer(t)
	cfg.MaxConnections = 1
	cfg.MaxConnectionsPerIP = 1
	cfg.SessionIdleTimeout = 150 * time.Millisecond
	h := mustHandler(t, cfg)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	opts := &websocket.DialOptions{
		HTTPHeader:   bearerHeader("server secret"),
		Host:         testHost,
		Subprotocols: []string{testSubprotocol()},
	}

	idle, _, err := websocket.Dial(t.Context(), websocketURL(server.URL), opts)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	t.Cleanup(func() { _ = idle.CloseNow() })
	waitForActiveSessions(t, h, 1)

	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		conn, _, err := websocket.Dial(ctx, websocketURL(server.URL), opts)
		cancel()
		if err == nil {
			_ = conn.CloseNow()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle session never released its slot: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestProxySessionDoesNotWaitForALingeringClose models the WebSocket adapter:
// its Close releases the copy goroutines immediately and only then lingers on
// the close handshake. The session must end with the copy goroutines, because
// everything waiting behind it - the capacity slot above all - is held until
// it returns.
func TestProxySessionDoesNotWaitForALingeringClose(t *testing.T) {
	inner, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		_ = clientPeer.Close()
		_ = upstreamPeer.Close()
	})
	client := &lingeringCloseConn{Conn: inner, release: release}

	finished := make(chan sessionStats, 1)
	go func() {
		finished <- proxySession(client, upstream, 100*time.Millisecond, 5*time.Second)
	}()

	select {
	case stats := <-finished:
		if stats.reason != "idle timeout" {
			t.Fatalf("close reason = %q, want idle timeout", stats.reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session waited for the lingering close")
	}
}

type lingeringCloseConn struct {
	net.Conn
	release chan struct{}
}

func (c *lingeringCloseConn) Close() error {
	err := c.Conn.Close()
	<-c.release
	return err
}
