//go:build live

package reality

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPublicRealityAuthorization checks the deployed Xray's authorization and
// destination ACL independently of the production client's fixed target guard.
func TestPublicRealityAuthorization(t *testing.T) {
	profileFile := os.Getenv("GUL_REALITY_LIVE_ADDRESS_FILE")
	passwordFile := os.Getenv("GUL_REALITY_LIVE_PASSWORD_FILE")
	if profileFile == "" || passwordFile == "" {
		t.Skip("private REALITY profile and password files are required")
	}
	profile, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatal("could not read private profile")
	}
	password, err := os.ReadFile(passwordFile)
	if err != nil {
		t.Fatal("could not read private password")
	}
	defer clear(password)
	u, err := url.Parse(strings.TrimSpace(string(profile)))
	if err != nil || u.Scheme != "vless" {
		t.Fatal("invalid private profile")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	q := u.Query()
	cfg := Config{Server: net.JoinHostPort(u.Hostname(), port), ServerName: q.Get("sni"),
		PublicKey: q.Get("pbk"), ShortID: q.Get("sid"),
		Password: strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r")}
	for _, tc := range []struct {
		name, target           string
		wrongPassword, allowed bool
	}{
		{"allowed_mumble", "127.0.0.1:64738", false, true},
		{"wrong_password", "127.0.0.1:64738", true, false},
		{"blocked_port", "127.0.0.1:64739", false, false},
		{"blocked_address", "127.0.0.2:64738", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := cfg
			if tc.wrongPassword {
				probe.Password += "-invalid"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			conn, err := dialTarget(ctx, probe, tc.target)
			if err != nil {
				t.Fatal("REALITY setup failed before the authorization probe")
			}
			defer conn.Close()
			// This probe only asks whether routing reaches Mumble TLS. The normal
			// public Mumble tests separately exercise the real TOFU verification.
			inner := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
			err = inner.HandshakeContext(ctx)
			if (err == nil) != tc.allowed {
				t.Fatal("server authorization or destination ACL disagreed with the expected route")
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("authorization probe was inconclusive: timed out")
			}
		})
	}
}
