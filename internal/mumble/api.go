package mumble

import "github.com/LywwKkA-aD/Gul/internal/domain"

// RawMessage is an incoming chat message before sanitization. The HTML field
// is untrusted server/user input: core sanitizes it before it reaches the UI.
type RawMessage struct {
	ChannelID  uint32
	Sender     string
	SenderHash string
	HTML       string
}

// Callbacks deliver session state to the owner (core). All callbacks are
// invoked from session goroutines and must return quickly: no blocking I/O,
// no calls back into the Manager.
type Callbacks struct {
	OnStatus  func(domain.ConnectionStatus)
	OnLatency func(domain.ConnectionLatency)
	OnTree    func(domain.ChannelNode)
	OnMessage func(RawMessage)
	OnTofu    func(domain.TofuPrompt)
}

// Controller is the surface core uses to drive the Mumble layer.
// *Manager implements it; tests may substitute a fake.
type Controller interface {
	// Connect starts an asynchronous connection attempt. Lifecycle progress
	// is reported through Callbacks.OnStatus; auto-reconnect with exponential
	// backoff (1s..30s cap) runs until Disconnect is called. After a
	// reconnect the last joined channel is restored.
	Connect(address, username, password string)
	// Disconnect stops the session and any reconnect loop.
	Disconnect()
	// Join moves self to the channel. Requires a connected session.
	Join(channelID uint32) error
	// SendMessage sends plain text to the channel; HTML metacharacters are
	// escaped inside (Mumble text messages are HTML).
	SendMessage(channelID uint32, text string) error
	// AcceptFingerprint confirms the pending TOFU mismatch (OnTofu) and
	// retries the connection with the new pinned fingerprint.
	AcceptFingerprint()
	// Status returns the current connection status snapshot.
	Status() domain.ConnectionStatus
	// Close releases resources; the Manager must not be used afterwards.
	Close()
}

var _ Controller = (*Manager)(nil)
