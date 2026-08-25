package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve runs a stub GitHub for one exchange and returns its endpoint.
func serve(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL + "/releases?per_page=1"
}

func TestLatestReadsTheFirstEntry(t *testing.T) {
	t.Parallel()
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// Newest first, as the list endpoint orders them.
		_, _ = w.Write([]byte(`[{"tag_name":"v0.4.0-alpha.1","html_url":"https://example.invalid/evil"},
		                        {"tag_name":"v0.3.0-alpha.2"}]`))
	})

	got, err := Latest(t.Context(), nil, endpoint)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Tag != "v0.4.0-alpha.1" || got.Version != "0.4.0-alpha.1" {
		t.Errorf("release = %+v", got)
	}
	// The page is ours to build: html_url is a URL off the network, and this
	// one would have sent the user somewhere else entirely.
	if want := releasePagePrefix + "v0.4.0-alpha.1"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
}

// The trap this whole endpoint choice exists for: /releases/latest excludes
// prereleases and answers 404 on a repository whose releases are all alphas.
// Whatever the reason, a status that is not 200 is silence.
func TestLatestOnNotFound(t *testing.T) {
	t.Parallel()
	endpoint := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})

	if _, err := Latest(t.Context(), nil, endpoint); err == nil {
		t.Fatal("Latest on 404 = nil error, want an error")
	}
}

func TestLatestOnRateLimit(t *testing.T) {
	t.Parallel()
	endpoint := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})

	if _, err := Latest(t.Context(), nil, endpoint); err == nil {
		t.Fatal("Latest on 403 = nil error, want an error")
	}
}

func TestLatestOnMalformedBody(t *testing.T) {
	t.Parallel()
	bodies := map[string]string{
		"not json":                    `<html><body>502 Bad Gateway</body></html>`,
		"an object":                   `{"tag_name":"v0.4.0"}`,
		"an empty list":               `[]`,
		"a missing tag":               `[{"name":"Gul 0.4.0"}]`,
		"a tag that is not a version": `[{"tag_name":"latest"}]`,
		"a null list":                 `null`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			endpoint := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			if got, err := Latest(t.Context(), nil, endpoint); err == nil {
				t.Fatalf("Latest = %+v, nil error; want an error", got)
			}
		})
	}
}

func TestLatestSkipsADraft(t *testing.T) {
	t.Parallel()
	endpoint := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v9.9.9","draft":true}]`))
	})
	if _, err := Latest(t.Context(), nil, endpoint); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("Latest on a draft = %v, want ErrNoRelease", err)
	}
}

// A body larger than the cap must not be swallowed whole. The decoder stops
// at the limit, which shows up as a decode failure - silence, as always.
func TestLatestCapsTheBody(t *testing.T) {
	t.Parallel()
	endpoint := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v0.4.0","body":"`))
		junk := strings.Repeat("a", 64*1024)
		for range (maxBodyBytes / len(junk)) + 2 {
			_, _ = w.Write([]byte(junk))
		}
		_, _ = w.Write([]byte(`"}]`))
	})
	if _, err := Latest(t.Context(), nil, endpoint); err == nil {
		t.Fatal("Latest on an oversized body = nil error, want an error")
	}
}

func TestLatestHonoursTheDeadline(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Latest(ctx, nil, endpoint); err == nil {
		t.Fatal("Latest against a server that never answers = nil error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Latest waited %v, want the deadline to cut it short", elapsed)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	endpoint := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v0.4.0-alpha.1"}]`))
	})

	t.Run("announces a newer release", func(t *testing.T) {
		release, ok, err := Check(t.Context(), nil, endpoint, "0.3.0-alpha.2", "")
		if err != nil || !ok {
			t.Fatalf("Check = %+v, %v, %v", release, ok, err)
		}
		if release.Version != "0.4.0-alpha.1" {
			t.Errorf("version = %q", release.Version)
		}
	})

	t.Run("stays quiet on the running version", func(t *testing.T) {
		if _, ok, err := Check(t.Context(), nil, endpoint, "0.4.0-alpha.1", ""); ok || err != nil {
			t.Fatalf("Check = %v, %v; want false, nil", ok, err)
		}
	})

	t.Run("stays quiet once dismissed", func(t *testing.T) {
		if _, ok, err := Check(t.Context(), nil, endpoint, "0.3.0-alpha.2", "v0.4.0-alpha.1"); ok || err != nil {
			t.Fatalf("Check = %v, %v; want false, nil", ok, err)
		}
	})

	t.Run("speaks again for a version newer than the dismissal", func(t *testing.T) {
		if _, ok, err := Check(t.Context(), nil, endpoint, "0.3.0-alpha.2", "v0.3.0-alpha.9"); !ok || err != nil {
			t.Fatalf("Check = %v, %v; want true, nil", ok, err)
		}
	})
}

func TestCheckIsSilentWhenTheNetworkIsGone(t *testing.T) {
	t.Parallel()
	// A port nothing listens on: the dial fails immediately.
	if _, ok, err := Check(t.Context(), nil, "http://127.0.0.1:1/releases", "0.3.0-alpha.2", ""); ok {
		t.Fatalf("Check = true, %v; want false", err)
	}
}

// DefaultEndpoint is the whole feature in one constant, and the wrong value
// fails silently forever: /releases/latest excludes prereleases and answers
// 404 on this repository, because every release so far is an alpha.
func TestDefaultEndpointSeesPrereleases(t *testing.T) {
	if strings.Contains(DefaultEndpoint, "/releases/latest") {
		t.Fatalf("DefaultEndpoint = %q: /releases/latest never sees a prerelease", DefaultEndpoint)
	}
	if !strings.Contains(DefaultEndpoint, "/releases?") {
		t.Fatalf("DefaultEndpoint = %q, want the list endpoint", DefaultEndpoint)
	}
}
