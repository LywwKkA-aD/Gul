package services

import (
	"context"
	"errors"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

type screenGrantProvider interface {
	ScreenGrant(context.Context, uint64, uint32) (domain.ScreenGrant, error)
}

type screenBrowser interface {
	Open(context.Context, uint64, uint32) error
}

// ScreenShareService issues grants or opens a browser for the confirmed voice channel.
// The manager validates the epoch again after the broker request completes.
type ScreenShareService struct {
	provider screenGrantProvider
	browser  screenBrowser
}

func NewScreenShareService(provider screenGrantProvider, browser ...screenBrowser) *ScreenShareService {
	service := &ScreenShareService{provider: provider}
	if len(browser) > 0 {
		service.browser = browser[0]
	}
	return service
}

func (s *ScreenShareService) Grant(ctx context.Context, epoch uint64, channelID uint32) (domain.ScreenGrant, error) {
	return s.provider.ScreenGrant(ctx, epoch, channelID)
}

// OpenBrowser is invoked only by the user's screen button, never on join.
func (s *ScreenShareService) OpenBrowser(ctx context.Context, epoch uint64, channelID uint32) error {
	if s.browser == nil {
		return errors.New("демонстрации в браузере недоступны")
	}
	return s.browser.Open(ctx, epoch, channelID)
}
