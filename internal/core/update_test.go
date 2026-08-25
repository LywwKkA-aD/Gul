package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// waitForUpdateEvent gives the check goroutine a moment to publish. The check
// is deliberately off the startup path, so every assertion about it is an
// assertion about something that happens on another goroutine.
func waitForUpdateEvent(t *testing.T, em *fakeEmitter) (domain.UpdateAvailable, bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range em.all() {
			if ev.name == domain.EventUpdateAvailable {
				available, ok := ev.payload.(domain.UpdateAvailable)
				if !ok {
					t.Fatalf("payload = %T, want domain.UpdateAvailable", ev.payload)
				}
				return available, true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return domain.UpdateAvailable{}, false
}

// releaseStub answers like the GitHub list endpoint and counts requests.
func releaseStub(t *testing.T, body string) (endpoint string, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, hits
}

// A version far above anything this build could be: the assertion is about the
// mechanism, not about the constant, which moves with every release.
const futureRelease = `[{"tag_name":"v99.0.0"}]`

func TestUpdateCheckAnnouncesANewerVersion(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	endpoint, hits := releaseStub(t, futureRelease)
	app.SetUpdateSource(endpoint, nil)

	app.StartUpdateCheck()
	available, ok := waitForUpdateEvent(t, em)
	if !ok {
		t.Fatal("no update event")
	}
	if available.Version != "99.0.0" || available.Tag != "v99.0.0" {
		t.Errorf("available = %+v", available)
	}
	if available.URL == "" {
		t.Error("no release page to send the user to")
	}
	// The snapshot is what a window that mounted late reads.
	if got := app.PendingUpdate(); got != available {
		t.Errorf("PendingUpdate = %+v, want %+v", got, available)
	}
	// One check per run: no polling.
	if got := hits.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestUpdateCheckIsSilentWhenTheReleaseIsNotNewer(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	endpoint, _ := releaseStub(t, `[{"tag_name":"v`+Version+`"}]`)
	app.SetUpdateSource(endpoint, nil)

	app.StartUpdateCheck()
	if available, ok := waitForUpdateEvent(t, em); ok {
		t.Fatalf("announced %+v for the running version", available)
	}
	if got := app.PendingUpdate(); got != (domain.UpdateAvailable{}) {
		t.Errorf("PendingUpdate = %+v, want the zero value", got)
	}
}

// Every failure is silence: this is the whole error policy of the feature.
func TestUpdateCheckIsSilentOnFailure(t *testing.T) {
	t.Parallel()
	cases := map[string]http.HandlerFunc{
		// The trap: /releases/latest excludes prereleases and 404s here. Any
		// 404 reads the same way.
		"not found": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		},
		"rate limited": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
		"a shape we did not expect": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
		},
		"a body that is not json": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html>502</html>`))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			app, _, em := newTestApp(t)
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)
			app.SetUpdateSource(srv.URL, nil)

			app.StartUpdateCheck()
			// Long enough for the goroutine to have finished and published,
			// had it been going to.
			time.Sleep(150 * time.Millisecond)
			if em.count(domain.EventUpdateAvailable) != 0 {
				t.Fatalf("announced something on %q", name)
			}
		})
	}
}

func TestDismissedVersionStaysQuietAndANewerOneDoesNot(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	endpoint, _ := releaseStub(t, `[{"tag_name":"v99.0.0"}]`)
	app.SetUpdateSource(endpoint, nil)

	app.StartUpdateCheck()
	if _, ok := waitForUpdateEvent(t, em); !ok {
		t.Fatal("no update event on the first run")
	}
	if err := app.DismissUpdate("v99.0.0"); err != nil {
		t.Fatalf("DismissUpdate: %v", err)
	}
	// Dismissing clears the snapshot too: the line is gone, not just unseen.
	if got := app.PendingUpdate(); got != (domain.UpdateAvailable{}) {
		t.Errorf("PendingUpdate after dismiss = %+v", got)
	}
	if got := app.Settings().Update.DismissedVersion; got != "v99.0.0" {
		t.Errorf("dismissed_version = %q", got)
	}

	// A second run against the same release says nothing.
	app2, _, em2 := newTestApp(t)
	app2.updateSettingsForTest(func(dismissed *string) { *dismissed = "v99.0.0" })
	app2.SetUpdateSource(endpoint, nil)
	app2.StartUpdateCheck()
	time.Sleep(150 * time.Millisecond)
	if em2.count(domain.EventUpdateAvailable) != 0 {
		t.Fatal("a dismissed version spoke again")
	}

	// A newer one is a new fact and speaks.
	newerEndpoint, _ := releaseStub(t, `[{"tag_name":"v99.1.0"}]`)
	app3, _, em3 := newTestApp(t)
	app3.updateSettingsForTest(func(dismissed *string) { *dismissed = "v99.0.0" })
	app3.SetUpdateSource(newerEndpoint, nil)
	app3.StartUpdateCheck()
	available, ok := waitForUpdateEvent(t, em3)
	if !ok {
		t.Fatal("a version newer than the dismissal stayed quiet")
	}
	if available.Version != "99.1.0" {
		t.Errorf("available = %+v", available)
	}
}

func TestDismissUpdateRejectsWhatIsNotAVersion(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	if err := app.DismissUpdate("latest"); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("DismissUpdate(latest) = %v, want ErrInvalidVersion", err)
	}
	if got := app.Settings().Update.DismissedVersion; got != "" {
		t.Errorf("dismissed_version = %q, want empty", got)
	}
}

// Shutdown must not wait on GitHub. The stub never answers; the exit path is
// expected to come back at once.
func TestShutdownDoesNotWaitForTheVersionCheck(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	app.SetUpdateSource(srv.URL, nil)

	app.StartUpdateCheck()
	done := make(chan struct{})
	go func() {
		app.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown blocked on the version check")
	}
}

// updateSettingsForTest seeds a dismissal without going through the public
// path, which would also schedule a write.
func (a *App) updateSettingsForTest(fn func(dismissed *string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(&a.cfg.Update.DismissedVersion)
}
