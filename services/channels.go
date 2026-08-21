package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// ChannelsService is the thin Wails bridge for channel navigation.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
type ChannelsService struct {
	app *core.App
}

func NewChannelsService(app *core.App) *ChannelsService {
	return &ChannelsService{app: app}
}

// Join moves self into the channel. Live updates arrive via channels:tree.
func (s *ChannelsService) Join(channelID uint32) error {
	return s.app.Join(channelID)
}

// Tree returns the last known snapshot, for a UI that mounted mid-session.
func (s *ChannelsService) Tree() domain.ChannelNode {
	return s.app.Tree()
}
