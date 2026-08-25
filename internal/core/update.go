package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/update"
)

// The startup version check (PLAN.md 7 M4+). One request, once, when the
// application starts. No polling, no automatic download, no second attempt.
//
// Three rules hold it in place:
//   - It runs off the startup path, on its own goroutine, so the window never
//     waits for GitHub.
//   - Every failure is silence. The user hears about a new version or hears
//     nothing at all; a courtesy that reports its own failures is a nag.
//   - It never delays the exit: the request carries a deadline of its own and
//     Shutdown cancels it, and nothing waits for the goroutine either way.

// ErrInvalidVersion reports a dismissal for something that is not a version.
var ErrInvalidVersion = errors.New("invalid version")

// SetUpdateSource points the check at another endpoint and client. Only tests
// call it; production uses the GitHub endpoint and a client with the package
// deadline.
func (a *App) SetUpdateSource(endpoint string, client *http.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.updateEndpoint, a.updateClient = endpoint, client
}

// StartUpdateCheck launches the one version check of this run. It returns
// immediately; the answer, if there is one worth showing, arrives as
// domain.EventUpdateAvailable.
func (a *App) StartUpdateCheck() {
	ctx, cancel := context.WithCancel(context.Background())

	a.mu.Lock()
	if a.updateCancel != nil {
		// A second call would leak the first check's context.
		a.mu.Unlock()
		cancel()
		return
	}
	a.updateCancel = cancel
	a.mu.Unlock()

	go func() {
		defer cancel()
		a.runUpdateCheck(ctx)
	}()
}

// runUpdateCheck performs the request and publishes what it found.
func (a *App) runUpdateCheck(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, update.RequestTimeout)
	defer cancel()

	a.mu.Lock()
	endpoint, client, dismissed := a.updateEndpoint, a.updateClient, a.cfg.Update.DismissedVersion
	a.mu.Unlock()
	if client == nil {
		client = &http.Client{Timeout: update.RequestTimeout}
	}

	release, announce, err := update.Check(ctx, client, endpoint, Version, dismissed)
	if err != nil {
		// Debug, not warn: no network is the normal state of a laptop in a
		// train, and this line is for us, not for the user.
		a.log.Debug("version check", "error", err)
		return
	}
	if !announce {
		return
	}

	available := domain.UpdateAvailable{
		Version: release.Version,
		Tag:     release.Tag,
		URL:     release.URL,
	}
	a.mu.Lock()
	a.pendingUpdate = available
	a.mu.Unlock()

	a.log.Info("newer version available", "version", available.Version)
	a.emit(domain.EventUpdateAvailable, available)
}

// PendingUpdate returns the version the check found, or the zero value when
// there is nothing to show.
//
// The event fires as soon as the answer arrives, which can be before the UI
// has subscribed to anything: a freshly mounted window asks for the snapshot
// instead of waiting for an event that already happened.
func (a *App) PendingUpdate() domain.UpdateAvailable {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingUpdate
}

// DismissUpdate records that the user does not want to hear about this
// version again. That one stays quiet forever; a newer one speaks up.
func (a *App) DismissUpdate(version string) error {
	version = strings.TrimSpace(version)
	if _, ok := update.Parse(version); !ok {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, version)
	}

	a.mu.Lock()
	if a.pendingUpdate.Version == version || a.pendingUpdate.Tag == version {
		a.pendingUpdate = domain.UpdateAvailable{}
	}
	a.mu.Unlock()

	a.updateSettings(func(c *config.Config) { c.Update.DismissedVersion = version })
	return nil
}

// stopUpdateCheck cancels a check still in flight. Called from Shutdown: the
// exit must not wait on a socket to GitHub.
func (a *App) stopUpdateCheck() {
	a.mu.Lock()
	cancel := a.updateCancel
	a.updateCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
