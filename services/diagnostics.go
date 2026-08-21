package services

import "github.com/LywwKkA-aD/Gul/internal/core"

// DiagnosticsService is the thin Wails bridge for the support bundle.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
type DiagnosticsService struct {
	app *core.App
}

func NewDiagnosticsService(app *core.App) *DiagnosticsService {
	return &DiagnosticsService{app: app}
}

// Collect writes a zip with logs and environment info, returning its path.
func (s *DiagnosticsService) Collect() (string, error) {
	return s.app.Collect()
}
