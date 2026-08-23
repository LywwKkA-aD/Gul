package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync/atomic"
	"time"
)

const (
	// defaultSessionIdleTimeout closes a session that moved no bytes in either
	// direction for this long. A live Mumble session pings every few seconds,
	// so only a wedged peer trips it. Without it, a client that stops reading
	// and stops writing holds its capacity slot until the process restarts.
	defaultSessionIdleTimeout = 60 * time.Second
	// defaultSessionWriteTimeout bounds one blocked write. A peer that stops
	// reading stalls the opposite direction as soon as the socket buffers
	// fill; the deadline turns that stall into a closed session.
	defaultSessionWriteTimeout = 30 * time.Second
	// proxyBufferBytes is allocated twice per session and reused for every
	// copy, so relaying costs no allocation per packet.
	proxyBufferBytes = 32 << 10
)

const (
	sideClient   = "client"
	sideUpstream = "upstream"
)

// sessionStats describes a finished session for its close log line.
type sessionStats struct {
	fromClient int64
	toClient   int64
	reason     string
	err        error
}

type copyOutcome struct {
	side string
	n    int64
	err  error
}

// proxySession copies bytes both ways until one direction ends, a write
// blocks past writeTimeout, or nothing moves for idle. It returns what the
// session transferred and why it ended.
func proxySession(client, upstream net.Conn, idle, writeTimeout time.Duration) sessionStats {
	if idle <= 0 {
		idle = defaultSessionIdleTimeout
	}
	if writeTimeout <= 0 {
		writeTimeout = defaultSessionWriteTimeout
	}

	var activity atomic.Int64
	activity.Store(time.Now().UnixNano())
	var idleTripped atomic.Bool

	results := make(chan copyOutcome, 2)
	pump := func(side string, dst, src net.Conn) {
		buffer := make([]byte, proxyBufferBytes)
		n, err := copyWithWriteDeadline(dst, src, buffer, &activity, writeTimeout)
		results <- copyOutcome{side: side, n: n, err: err}
	}
	go pump(sideClient, upstream, client)
	go pump(sideUpstream, client, upstream)

	stop := make(chan struct{})
	watchdog := make(chan struct{})
	go func() {
		defer close(watchdog)
		watchIdle(stop, idle, &activity, &idleTripped, client, upstream)
	}()

	first := <-results
	// The client side is a WebSocket adapter whose Close performs a close
	// handshake, which a peer that already went away never answers. Its first
	// act is to cancel the stream contexts, so the copy goroutines return
	// either way; waiting for the handshake would only hold the session's
	// capacity slot while the courtesy close frame times out.
	go func() { _ = client.Close() }()
	_ = upstream.Close()
	second := <-results
	close(stop)
	<-watchdog

	stats := sessionStats{reason: closeReason(first, idleTripped.Load()), err: first.err}
	for _, outcome := range [...]copyOutcome{first, second} {
		if outcome.side == sideClient {
			stats.fromClient = outcome.n
		} else {
			stats.toClient = outcome.n
		}
	}
	return stats
}

// copyWithWriteDeadline is io.Copy with a deadline around every write and an
// activity stamp the idle watchdog reads. It cannot use io.Copy because the
// deadline has to be refreshed per write, and it reuses one buffer so a busy
// session allocates nothing.
func copyWithWriteDeadline(dst, src net.Conn, buffer []byte, activity *atomic.Int64, writeTimeout time.Duration) (int64, error) {
	var total int64
	for {
		read, readErr := src.Read(buffer)
		if read > 0 {
			activity.Store(time.Now().UnixNano())
			if err := dst.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return total, err
			}
			written, writeErr := dst.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			activity.Store(time.Now().UnixNano())
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}

func watchIdle(stop <-chan struct{}, idle time.Duration, activity *atomic.Int64, tripped *atomic.Bool, conns ...net.Conn) {
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
			last := time.Unix(0, activity.Load())
			if remaining := idle - time.Since(last); remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			tripped.Store(true)
			for _, conn := range conns {
				// Closing a WebSocket lingers on its close handshake. The
				// watchdog must not linger with it: the copy goroutines are
				// released by the same call, and the session ends with them.
				go func(conn net.Conn) { _ = conn.Close() }(conn)
			}
			return
		}
	}
}

func closeReason(outcome copyOutcome, idle bool) string {
	switch {
	case idle:
		return "idle timeout"
	case isTimeout(outcome.err):
		return "write timeout"
	case outcome.err == nil:
		return outcome.side + " closed"
	case errors.Is(outcome.err, context.Canceled):
		return "relay shutdown"
	default:
		return outcome.side + " error"
	}
}

// isTimeout covers both deadline flavors: net conns report
// os.ErrDeadlineExceeded, while the WebSocket net.Conn adapter reports the
// context deadline it cancels the write with.
func isTimeout(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
}
