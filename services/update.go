package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// UpdateService is the thin Wails bridge for the startup version check.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
//
// The check runs once, in Go, when the application starts. This service is
// only how the window reads its result and how the user silences it.
type UpdateService struct {
	app *core.App
}

func NewUpdateService(app *core.App) *UpdateService {
	return &UpdateService{app: app}
}

// Available returns the newer version found at startup, or the zero value
// when there is nothing to show.
//
// The check finishes on its own schedule, which can be before the window has
// subscribed to anything, so the UI reads this on mount instead of waiting
// for an event that may already have happened.
func (s *UpdateService) Available() domain.UpdateAvailable {
	return s.app.PendingUpdate()
}

// Dismiss silences one version for good. Anything newer speaks up again on a
// later start.
func (s *UpdateService) Dismiss(version string) error {
	return s.app.DismissUpdate(version)
}
