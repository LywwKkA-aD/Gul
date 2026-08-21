package services

import (
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// ChatService is the thin Wails bridge for text chat.
// No logic here: marshal and delegate to core (PLAN.md §10.4).
type ChatService struct {
	app *core.App
}

func NewChatService(app *core.App) *ChatService {
	return &ChatService{app: app}
}

// Send posts plain text to a channel; the echo arrives via chat:message.
func (s *ChatService) Send(channelID uint32, text string) error {
	return s.app.SendMessage(channelID, text)
}

// History returns the session transcript of one channel, oldest first.
func (s *ChatService) History(channelID uint32) []domain.ChatMessage {
	return s.app.History(channelID)
}
