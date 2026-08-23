package relay

import (
	"container/list"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// Credential classes, used for logging. They describe the shape of what was
// presented, never its value.
const (
	classMissing   = "missing"
	classMalformed = "malformed"
	classLegacy    = "legacy"
	classV2        = "v2"
)

type authResult struct {
	authorized bool
	legacy     bool
	class      string
}

// authorize compares the Authorization header against the precomputed
// expected credentials. Deriving a credential costs tens of milliseconds, so
// nothing on this path derives anything: it parses, then compares digests in
// constant time. Every expected credential is compared, without an early
// return, so the work does not depend on which one matches.
func (h *Handler) authorize(header string) authResult {
	if header == "" {
		return authResult{class: classMissing}
	}
	presented, ok := relayproto.ParseHeader(header)
	if !ok {
		return authResult{class: classMalformed}
	}
	result := authResult{class: classV2}
	if presented.Legacy() {
		result.class = classLegacy
	}
	for _, expected := range h.credentials {
		if presented.Matches(expected) {
			result.authorized = true
			result.legacy = expected.Legacy()
		}
	}
	return result
}

// prepareCredentials validates the precomputed expected credentials and
// returns the set the relay honors together with the v2 credential that keys
// source pseudonymization. Legacy credentials are dropped unless the
// deprecation window is still open.
func prepareCredentials(credentials []relayproto.Credential, acceptLegacy bool) ([]relayproto.Credential, relayproto.Credential, error) {
	if len(credentials) == 0 {
		return nil, "", errors.New("at least one expected credential is required")
	}
	honored := make([]relayproto.Credential, 0, len(credentials))
	var primary relayproto.Credential
	for _, credential := range credentials {
		if _, ok := relayproto.ParseHeader(credential.Header()); !ok {
			return nil, "", errors.New("must be values produced by `gul-relay derive-credential`")
		}
		if credential.Legacy() {
			if acceptLegacy {
				honored = append(honored, credential)
			}
			continue
		}
		if primary == "" {
			primary = credential
		}
		honored = append(honored, credential)
	}
	if primary == "" {
		return nil, "", errors.New("a v2 credential is required; re-run `gul-relay derive-credential`")
	}
	return honored, primary, nil
}

type authFailureLimiter struct {
	mu                sync.Mutex
	entries           map[string]*authFailureEntry
	order             *list.List
	failuresBeforeBan int
	failureWindow     time.Duration
	banDuration       time.Duration
	maxEntries        int
	now               func() time.Time
}

type authFailureEntry struct {
	source        string
	failures      int
	windowStarted time.Time
	bannedUntil   time.Time
	element       *list.Element
}

func newAuthFailureLimiter(cfg Config) *authFailureLimiter {
	return &authFailureLimiter{
		entries:           make(map[string]*authFailureEntry),
		order:             list.New(),
		failuresBeforeBan: cfg.AuthFailuresBeforeBan,
		failureWindow:     cfg.AuthFailureWindow,
		banDuration:       cfg.AuthBanDuration,
		maxEntries:        cfg.MaxAuthTrackedSources,
		now:               cfg.Now,
	}
}

// banState is the limiter's verdict for one failed request. activated marks
// the request that turned the ban on, which is the one worth a log line: the
// rejections that follow are driven by whoever keeps knocking.
type banState struct {
	retryAfter time.Duration
	limited    bool
	activated  bool
}

// recordFailure bans a source once it consumes its configured allowance of
// 401 responses. The triggering request and all later failed requests receive
// 429 until the fixed ban expires.
//
// The key is a folded source block (sourceKey), not one address: an IPv6
// subscriber holds a /64 and would otherwise spend a fresh allowance of
// guesses per address. That block can also be a whole NAT, so the ban is
// deliberately short and always accompanied by Retry-After: it has to blunt
// online password guessing without turning one mistyped password into a
// lasting outage for the network behind it.
func (l *authFailureLimiter) recordFailure(source string) banState {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[source]
	if !ok {
		entry = l.insert(source, now)
	} else {
		l.order.MoveToBack(entry.element)
	}
	if now.Before(entry.bannedUntil) {
		return banState{retryAfter: entry.bannedUntil.Sub(now), limited: true}
	}
	if !entry.bannedUntil.IsZero() || !now.Before(entry.windowStarted.Add(l.failureWindow)) {
		entry.failures = 0
		entry.windowStarted = now
		entry.bannedUntil = time.Time{}
	}

	entry.failures++
	if entry.failures > l.failuresBeforeBan {
		entry.bannedUntil = now.Add(l.banDuration)
		return banState{retryAfter: l.banDuration, limited: true, activated: true}
	}
	return banState{}
}

func (l *authFailureLimiter) banRemaining(source string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[source]
	if !ok {
		return 0, false
	}
	if now.Before(entry.bannedUntil) {
		l.order.MoveToBack(entry.element)
		return entry.bannedUntil.Sub(now), true
	}
	if !entry.bannedUntil.IsZero() {
		delete(l.entries, source)
		l.order.Remove(entry.element)
	}
	return 0, false
}

func (l *authFailureLimiter) clearIfAllowed(source string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[source]
	if !ok {
		return 0, false
	}
	if now.Before(entry.bannedUntil) {
		l.order.MoveToBack(entry.element)
		return entry.bannedUntil.Sub(now), true
	}
	delete(l.entries, source)
	l.order.Remove(entry.element)
	return 0, false
}

func (l *authFailureLimiter) insert(source string, now time.Time) *authFailureEntry {
	if len(l.entries) >= l.maxEntries {
		oldestElement := l.order.Front()
		oldest := oldestElement.Value.(*authFailureEntry)
		delete(l.entries, oldest.source)
		l.order.Remove(oldestElement)
	}
	entry := &authFailureEntry{source: source, windowStarted: now}
	entry.element = l.order.PushBack(entry)
	l.entries[source] = entry
	return entry
}

func retryAfterSeconds(duration time.Duration) string {
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(int64(seconds), 10)
}

// writeRateLimited answers a temporarily banned source. Retry-After is always
// present so a legitimate client behind a shared address waits and retries
// instead of treating the rejection as final.
func writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	r.Close = true
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
}

// writeCapacityRejected answers a request that authenticated but found the
// relay full. Retry-After keeps a rejected client from hammering the endpoint.
func writeCapacityRejected(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
}
