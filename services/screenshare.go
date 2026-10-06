package services

import (
	"context"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

type screenGrantProvider interface {
	ScreenGrant(context.Context, uint64, uint32) (domain.ScreenGrant, error)
}

// ScreenShareService issues a companion grant for the confirmed voice channel.
// The manager validates the epoch again after the broker request completes.
type ScreenShareService struct{ provider screenGrantProvider }

func NewScreenShareService(provider screenGrantProvider) *ScreenShareService {
	return &ScreenShareService{provider: provider}
}

func (s *ScreenShareService) Grant(ctx context.Context, epoch uint64, channelID uint32) (domain.ScreenGrant, error) {
	return s.provider.ScreenGrant(ctx, epoch, channelID)
}
