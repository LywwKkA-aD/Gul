package services

import (
	"context"

	"github.com/LywwKkA-aD/Gul/internal/screensharelab"
)

// ScreenShareLabService exists only in the isolated, opt-in local lab mode.
type ScreenShareLabService struct{ client *screensharelab.Client }

func NewScreenShareLabService() *ScreenShareLabService {
	return &ScreenShareLabService{client: screensharelab.NewClient()}
}

func (s *ScreenShareLabService) Join(ctx context.Context, identity string) (screensharelab.Grant, error) {
	return s.client.Join(ctx, identity)
}
