package relay

import (
	"log/slog"
	"net"
	"sync"
	"time"
)

const (
	// DefaultPreAuthPerSource bounds the accepted TCP connections one source
	// may hold open. An established session keeps its connection, so a caller
	// must set this above the number of sessions it allows one source, or a
	// source at its session quota cannot even open a handshake.
	DefaultPreAuthPerSource = 8
	// DefaultPreAuthTotal bounds all accepted connections. Reaching it rejects
	// new connections immediately instead of suspending Accept, so no single
	// source can stop the listener.
	DefaultPreAuthTotal = 256

	rejectionReportInterval = 30 * time.Second
)

// SourceLimitedListener bounds concurrent accepted connections per source
// prefix and in total, before TLS and before authentication.
//
// It never stops accepting: connections over a limit are closed immediately
// and Accept moves on. A listener that blocks at its cap (the behavior of a
// plain concurrency-limited listener) can be taken offline by one source that
// opens the cap in idle keep-alive connections, because the kernel keeps
// completing handshakes into a backlog nothing drains.
type SourceLimitedListener struct {
	net.Listener

	perSource int
	total     int
	logger    *slog.Logger
	now       func() time.Time

	mu         sync.Mutex
	counts     map[string]int
	open       int
	rejected   int
	lastReport time.Time
}

// LimitListenerBySource wraps listener with per-source and total connection
// limits. Non-positive limits fall back to the defaults.
func LimitListenerBySource(listener net.Listener, perSource, total int, logger *slog.Logger) *SourceLimitedListener {
	if perSource <= 0 {
		perSource = DefaultPreAuthPerSource
	}
	if total <= 0 {
		total = DefaultPreAuthTotal
	}
	if total < perSource {
		total = perSource
	}
	return &SourceLimitedListener{
		Listener:  listener,
		perSource: perSource,
		total:     total,
		logger:    loggerOrDefault(logger),
		now:       time.Now,
		counts:    make(map[string]int),
	}
}

// Accept returns the next connection that fits within the limits. Rejected
// connections are closed before any TLS handshake work is spent on them.
func (l *SourceLimitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		key := sourcePrefixKey(conn.RemoteAddr())
		scope, ok := l.acquire(key)
		if !ok {
			_ = conn.Close()
			l.reportRejection(scope)
			continue
		}
		var once sync.Once
		limited := &limitedConn{Conn: conn, release: func() { once.Do(func() { l.release(key) }) }}
		return limited, nil
	}
}

// OpenConnections reports how many accepted connections are still open.
func (l *SourceLimitedListener) OpenConnections() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.open
}

func (l *SourceLimitedListener) acquire(key string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open >= l.total {
		return "total", false
	}
	if l.counts[key] >= l.perSource {
		return "source", false
	}
	l.counts[key]++
	l.open++
	return "", true
}

func (l *SourceLimitedListener) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[key] <= 1 {
		delete(l.counts, key)
	} else {
		l.counts[key]--
	}
	if l.open > 0 {
		l.open--
	}
}

// reportRejection logs at most one line per interval. Rejections are driven by
// the attacker, so an unthrottled line per connection would turn the limiter
// into a log amplifier.
func (l *SourceLimitedListener) reportRejection(scope string) {
	l.mu.Lock()
	l.rejected++
	now := l.now()
	if !l.lastReport.IsZero() && now.Sub(l.lastReport) < rejectionReportInterval {
		l.mu.Unlock()
		return
	}
	l.lastReport = now
	count := l.rejected
	l.rejected = 0
	open := l.open
	l.mu.Unlock()
	l.logger.Warn("relay pre-authentication connection rejected",
		"scope", scope,
		"rejected", count,
		"open", open,
		"per_source_limit", l.perSource,
		"total_limit", l.total,
	)
}

type limitedConn struct {
	net.Conn
	release func()
}

func (c *limitedConn) Close() error {
	c.release()
	return c.Conn.Close()
}
