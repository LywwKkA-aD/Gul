package mumble

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/gumble/gumble"
)

type reconnectCountedConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *reconnectCountedConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

func TestManagerClosesDroppedSessionBeforeBackoffAndRedial(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	counted := &reconnectCountedConn{Conn: clientSide}
	firstSession := &Session{packets: newPacketConn(counted)}
	defer firstSession.Disconnect()

	sink := newStatusSink()
	manager := newTestManager(t, Callbacks{OnStatus: sink.record})
	firstHooks := make(chan sessionHooks, 1)
	atBackoff := make(chan int32, 1)
	atRedial := make(chan int32, 1)
	var attempts int
	manager.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		attempts++
		if attempts == 1 {
			firstHooks <- hooks
			return firstSession, nil
		}
		atRedial <- counted.closes.Load()
		return &Session{}, nil
	}
	manager.backoffFn = func(int) time.Duration {
		atBackoff <- counted.closes.Load()
		return 0
	}
	manager.Connect("voice.example.test", "gul", "test-password")
	sink.expect(t, domain.StateConnecting)
	sink.expect(t, domain.StateConnected)

	select {
	case hooks := <-firstHooks:
		hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError})
	case <-time.After(time.Second):
		t.Fatal("the first session never opened")
	}
	sink.expect(t, domain.StateReconnecting)
	for _, check := range []struct {
		name   string
		counts <-chan int32
	}{
		{"before reconnect backoff", atBackoff},
		{"before the next dial", atRedial},
	} {
		select {
		case count := <-check.counts:
			if count != 1 {
				t.Fatalf("old transport closed %d times %s, want exactly once", count, check.name)
			}
		case <-time.After(time.Second):
			t.Fatalf("manager never reached %s", check.name)
		}
	}
	if err := serverSide.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if _, err := serverSide.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("dropped transport remained open: %v", err)
	}
	sink.expect(t, domain.StateConnected)
	manager.Disconnect()
	if count := counted.closes.Load(); count != 1 {
		t.Fatalf("Disconnect closed the retired transport %d times, want exactly once", count)
	}
}
