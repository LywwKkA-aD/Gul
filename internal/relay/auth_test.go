package relay

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

func TestHandlerAcceptsCurrentBearerCredential(t *testing.T) {
	logger, records := newRecordingLogger()
	cfg := baseConfig("server secret")
	cfg.Logger = logger

	assertAuthorized(t, mustHandler(t, cfg), records, "192.0.2.10", "server secret")
}

// assertAuthorized runs one attempt and requires that the handler accepted the
// credential. It cannot read that off the status: an accepted credential that
// stops at the next protocol check and a refused one both answer with the
// cover page, on purpose (cover.go). The rejection log is what separates them.
func assertAuthorized(
	t *testing.T,
	h http.Handler,
	records *recordingHandler,
	sourceIP, secret string,
) {
	t.Helper()
	before := records.count("relay authorization rejected")
	if got := serveAuthorizationAttempt(h, sourceIP, secret).Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want the same page every refusal gives", got)
	}
	if after := records.count("relay authorization rejected"); after != before {
		t.Fatalf("the credential was refused, want accepted")
	}
}

func TestHandlerAcceptsLegacyBearerCredentialOnlyWhileEnabled(t *testing.T) {
	const secret = "server secret"
	legacyHeader := make(http.Header)
	legacyHeader.Set("Authorization", testLegacyCredential(secret).Header())

	t.Run("enabled", func(t *testing.T) {
		logger, records := newRecordingLogger()
		cfg := baseConfig(secret)
		cfg.BearerCredentials = append(cfg.BearerCredentials, testLegacyCredential(secret))
		cfg.AcceptLegacyBearer = true
		cfg.Logger = logger
		h := mustHandler(t, cfg)

		serveWithHeader(h, "192.0.2.10", legacyHeader)
		record := records.await(t, "relay accepted legacy bearer credential")
		if got := recordAttrs(record)["source"]; got != "192.0.2.10" {
			t.Fatalf("logged source = %q, want 192.0.2.10", got)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		logger, records := newRecordingLogger()
		cfg := baseConfig(secret)
		cfg.BearerCredentials = append(cfg.BearerCredentials, testLegacyCredential(secret))
		cfg.AcceptLegacyBearer = false
		cfg.Logger = logger
		h := mustHandler(t, cfg)

		if got := serveWithHeader(h, "192.0.2.10", legacyHeader).Code; got != http.StatusNotFound {
			t.Fatalf("legacy credential status = %d, want 404", got)
		}
		// The current credential still works with the deprecation window shut.
		assertAuthorized(t, h, records, "192.0.2.11", secret)
	})
}

func TestHandlerRejectsMalformedAuthorizationHeaders(t *testing.T) {
	valid := string(testCredential("server secret"))
	tests := []struct {
		name  string
		value string
		class string
	}{
		{name: "empty", value: "", class: classMissing},
		{name: "scheme only", value: "Bearer", class: classMalformed},
		{name: "wrong scheme", value: "Basic " + valid, class: classMalformed},
		{name: "two tokens", value: "Bearer " + valid + " extra", class: classMalformed},
		{name: "padded base64", value: "Bearer " + valid + "==", class: classMalformed},
		{name: "oversized", value: "Bearer v2." + strings.Repeat("a", 200), class: classMalformed},
		{name: "wrong secret", value: testCredential("other secret").Header(), class: classV2},
		{name: "legacy shape", value: testLegacyCredential("server secret").Header(), class: classLegacy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger, records := newRecordingLogger()
			cfg := baseConfig("server secret")
			cfg.Logger = logger
			h := mustHandler(t, cfg)

			header := make(http.Header)
			if tc.value != "" {
				header.Set("Authorization", tc.value)
			}
			if got := serveWithHeader(h, "192.0.2.10", header).Code; got != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", got)
			}
			attrs := recordAttrs(records.await(t, "relay authorization rejected"))
			if attrs["credential"] != tc.class {
				t.Fatalf("logged credential class = %q, want %q", attrs["credential"], tc.class)
			}
			if attrs["source"] != "192.0.2.10" {
				t.Fatalf("logged source = %q, want 192.0.2.10", attrs["source"])
			}
			if rendered := records.rendered(); strings.Contains(rendered, valid) || (tc.value != "" && strings.Contains(rendered, tc.value)) {
				t.Fatalf("a log record carried the presented credential: %s", rendered)
			}
		})
	}
}

func TestNewHandlerRequiresCurrentCredential(t *testing.T) {
	legacy := testLegacyCredential("server secret")
	tests := []struct {
		name        string
		credentials []relayproto.Credential
		legacyOK    bool
	}{
		{name: "none", credentials: nil},
		{name: "legacy only", credentials: []relayproto.Credential{legacy}, legacyOK: true},
		{name: "legacy only with window closed", credentials: []relayproto.Credential{legacy}},
		{name: "raw password", credentials: []relayproto.Credential{relayproto.Credential("v2.not a credential")}},
		{name: "padded", credentials: []relayproto.Credential{relayproto.Credential(string(testCredential("server secret")) + "==")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig("server secret")
			cfg.BearerCredentials = tc.credentials
			cfg.AcceptLegacyBearer = tc.legacyOK
			if _, err := NewHandler(cfg); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// TestHandlerAuthorizationNeverDerivesPerRequest guards the contract that made
// PBKDF2 affordable: derivation happens when the secret is provisioned, never
// while answering a request. A single derivation must outlast hundreds of
// authorization attempts.
func TestHandlerAuthorizationNeverDerivesPerRequest(t *testing.T) {
	h := mustHandler(t, baseConfig("server secret"))
	header := testCredential("server secret").Header()

	start := time.Now()
	relayproto.Derive([]byte("timing reference secret"))
	derivation := time.Since(start)

	start = time.Now()
	for range 200 {
		if !h.authorize(header).authorized {
			t.Fatal("valid credential was rejected")
		}
	}
	authorizations := time.Since(start)

	if authorizations >= derivation {
		t.Fatalf("200 authorizations took %s, one derivation takes %s: the request path is deriving", authorizations, derivation)
	}
}

func TestHandlerDoesNotAcceptRawMumblePasswordAsBearerCredential(t *testing.T) {
	const rawPassword = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	h := mustHandler(t, baseConfig(rawPassword))

	header := make(http.Header)
	header.Set("Authorization", "Bearer "+rawPassword)
	if got := serveWithHeader(h, "192.0.2.10", header).Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got)
	}
}

func TestHandlerTemporarilyBansRepeatedAuthorizationFailures(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 2
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = 30 * time.Second
	cfg.MaxAuthTrackedSources = 16
	cfg.Now = func() time.Time { return now }
	h := mustHandler(t, cfg)

	for attempt := 1; attempt <= 2; attempt++ {
		response := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
		if response.Code != http.StatusNotFound {
			t.Fatalf("attempt %d status = %d, want 404", attempt, response.Code)
		}
	}

	limited := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d, want 429", limited.Code)
	}
	if got := limited.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
	if got := limited.Header().Get("Connection"); !strings.EqualFold(got, "close") {
		t.Fatalf("Connection = %q, want close", got)
	}
	if strings.Contains(limited.Body.String(), "wrong secret") {
		t.Fatal("response leaked the supplied credential")
	}

	now = now.Add(29*time.Second + time.Millisecond)
	limited = serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("status before ban expiry = %d, want 429", limited.Code)
	}
	if got := limited.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("rounded Retry-After = %q, want 1", got)
	}

	now = now.Add(time.Second)
	afterExpiry := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
	if afterExpiry.Code != http.StatusNotFound {
		t.Fatalf("status after ban expiry = %d, want 404", afterExpiry.Code)
	}
}

// TestHandlerRateLimitedResponsesAlwaysCarryRetryAfter covers the shared-source
// case: a NAT that trips the ban must always be told when to come back, so a
// legitimate client behind it waits instead of failing permanently.
func TestHandlerRateLimitedResponsesAlwaysCarryRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 1
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.Now = func() time.Time { return now }
	h := mustHandler(t, cfg)

	for attempt := range 4 {
		response := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret")
		if attempt == 0 {
			continue
		}
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want 429", attempt, response.Code)
		}
		if got := response.Header().Get("Retry-After"); got != "60" {
			t.Fatalf("attempt %d Retry-After = %q, want 60", attempt, got)
		}
	}
	// The correct password from the same shared address is delayed, not denied.
	valid := serveAuthorizationAttempt(h, "192.0.2.10", "server secret")
	if valid.Code != http.StatusTooManyRequests {
		t.Fatalf("valid credential during ban status = %d, want 429", valid.Code)
	}
	if got := valid.Header().Get("Retry-After"); got == "" {
		t.Fatal("valid credential during ban has no Retry-After")
	}
}

func TestHandlerAuthorizationFailuresExpireOutsideWindow(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 1
	cfg.AuthFailureWindow = 10 * time.Second
	cfg.AuthBanDuration = time.Minute
	cfg.MaxAuthTrackedSources = 16
	cfg.Now = func() time.Time { return now }
	h := mustHandler(t, cfg)

	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusNotFound {
		t.Fatalf("first failure status = %d, want 404", got)
	}
	now = now.Add(10 * time.Second)
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusNotFound {
		t.Fatalf("failure after window status = %d, want 404", got)
	}
}

func TestNewHandlerUsesSecureAuthorizationLimiterDefaults(t *testing.T) {
	h := mustHandler(t, baseConfig("server secret"))

	if h.authFailures.failuresBeforeBan != 5 || h.authFailures.failureWindow != time.Minute {
		t.Fatalf("unexpected failure window defaults: %#v", h.authFailures)
	}
	// The ban keys on a source address that a whole NAT can share, so it must
	// stay short enough to be a delay rather than an outage.
	if h.authFailures.banDuration != time.Minute {
		t.Fatalf("ban duration = %s, want 1m", h.authFailures.banDuration)
	}
	if h.authFailures.maxEntries != 4096 {
		t.Fatalf("tracked sources = %d, want 4096", h.authFailures.maxEntries)
	}
	if h.idleTimeout != defaultSessionIdleTimeout || h.writeTimeout != defaultSessionWriteTimeout {
		t.Fatalf("session timeouts = %s/%s", h.idleTimeout, h.writeTimeout)
	}
	if h.drainTimeout != defaultShutdownDrainTimeout {
		t.Fatalf("drain timeout = %s, want %s", h.drainTimeout, defaultShutdownDrainTimeout)
	}
}

func TestHandlerSuccessfulAuthorizationClearsPriorFailures(t *testing.T) {
	logger, records := newRecordingLogger()
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 2
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.MaxAuthTrackedSources = 16
	cfg.Logger = logger
	h := mustHandler(t, cfg)

	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusNotFound {
		t.Fatalf("first failure status = %d, want 404", got)
	}
	// A valid credential clears the source's incomplete failure window before
	// it becomes a ban.
	assertAuthorized(t, h, records, "192.0.2.10", "server secret")
	for attempt := 1; attempt <= 2; attempt++ {
		if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusNotFound {
			t.Fatalf("failure %d after success status = %d, want 404", attempt, got)
		}
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("third failure after success status = %d, want 429", got)
	}
}

func TestHandlerActiveBanRejectsValidAuthorizationUntilExpiry(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 1
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = 30 * time.Second
	cfg.MaxAuthTrackedSources = 16
	cfg.Now = func() time.Time { return now }
	logger, records := newRecordingLogger()
	cfg.Logger = logger
	h := mustHandler(t, cfg)

	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusNotFound {
		t.Fatalf("first failure status = %d, want 404", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("ban-triggering failure status = %d, want 429", got)
	}
	duringBan := serveAuthorizationAttempt(h, "192.0.2.10", "server secret")
	if duringBan.Code != http.StatusTooManyRequests {
		t.Fatalf("valid credential during ban status = %d, want 429", duringBan.Code)
	}
	if got := duringBan.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("valid credential Retry-After = %q, want 30", got)
	}

	now = now.Add(30 * time.Second)
	assertAuthorized(t, h, records, "192.0.2.10", "server secret")
}

func TestHandlerAuthorizationLimiterHasBoundedMemory(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 2
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.MaxAuthTrackedSources = 2
	h := mustHandler(t, cfg)

	for _, source := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.1", "192.0.2.3"} {
		_ = serveAuthorizationAttempt(h, source, "wrong secret")
	}

	h.authFailures.mu.Lock()
	defer h.authFailures.mu.Unlock()
	if got := len(h.authFailures.entries); got != 2 {
		t.Fatalf("tracked sources = %d, want 2", got)
	}
	if _, ok := h.authFailures.entries["192.0.2.2"]; ok {
		t.Fatal("least-recently-used source was not evicted")
	}
	if _, ok := h.authFailures.entries["192.0.2.1"]; !ok {
		t.Fatal("recently used source was evicted")
	}
}

func TestHandlerAuthorizationLimiterIsConcurrent(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 2
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.MaxAuthTrackedSources = 8
	h := mustHandler(t, cfg)

	var group sync.WaitGroup
	for attempt := range 100 {
		group.Add(1)
		go func(attempt int) {
			defer group.Done()
			source := net.IPv4(192, 0, 2, byte(attempt%16+1)).String()
			_ = serveAuthorizationAttempt(h, source, "wrong secret")
		}(attempt)
	}
	group.Wait()

	h.authFailures.mu.Lock()
	defer h.authFailures.mu.Unlock()
	if got := len(h.authFailures.entries); got > 8 {
		t.Fatalf("tracked sources = %d, want at most 8", got)
	}
}

func TestHandlerAuthorizationFailuresAreAtomicPerSource(t *testing.T) {
	cfg := baseConfig("server secret")
	cfg.AuthFailuresBeforeBan = 5
	cfg.AuthFailureWindow = time.Minute
	cfg.AuthBanDuration = time.Minute
	cfg.MaxAuthTrackedSources = 8
	h := mustHandler(t, cfg)

	var unauthorized atomic.Int64
	var limited atomic.Int64
	var unexpected atomic.Int64
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			switch serveAuthorizationAttempt(h, "192.0.2.10", "wrong secret").Code {
			case http.StatusNotFound:
				unauthorized.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	group.Wait()
	if got := unauthorized.Load(); got != 5 {
		t.Fatalf("404 responses = %d, want exactly 5", got)
	}
	if got := limited.Load(); got != 95 {
		t.Fatalf("429 responses = %d, want 95", got)
	}
	if got := unexpected.Load(); got != 0 {
		t.Fatalf("unexpected responses = %d, want 0", got)
	}
	if got := serveAuthorizationAttempt(h, "192.0.2.10", "server secret").Code; got != http.StatusTooManyRequests {
		t.Fatalf("valid credential during concurrent ban status = %d, want 429", got)
	}
}
