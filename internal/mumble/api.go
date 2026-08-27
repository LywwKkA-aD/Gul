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
	// OnTransport fires when a road has proved it carries our packets there
	// and back (transport.go). It is the only moment worth remembering: a
	// road that merely connected has proved nothing.
	OnTransport func(address, transport string)
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
	// SetSelfAudio publishes the local microphone and monitor state to the
	// server so other participants see it. Both flags travel together: the
	// server infers one from the other otherwise (see selfaudio.go). Offline
	// it records the intent, restored on the next connect.
	//
	// It must never block and never call back into core: core calls it while
	// holding the lock that decides the transition.
	SetSelfAudio(muted, deafened bool)
	// SelfAudioSettled reports whether the pair a channel tree carries can be
	// taken as the server's own opinion, rather than an echo of a state we
	// have already moved on from. It is false while an intent of ours is
	// unwritten AND for as long as a written one is still unacknowledged: the
	// packet reaches the socket long before the room answers, and a tree that
	// arrives in between still carries our previous flags.
	SelfAudioSettled(muted, deafened bool) bool
	// PreferTransport seeds the road to try first for one server, from what
	// was remembered about it. Call before Connect; an unknown road is
	// ignored, which leaves the ordinary search in place.
	PreferTransport(address, transport string)
	// AcceptFingerprint confirms the pending TOFU mismatch (OnTofu) and
	// retries the connection with the new pinned fingerprint.
	AcceptFingerprint()
	// Status returns the current connection status snapshot.
	Status() domain.ConnectionStatus
	// Close releases resources; the Manager must not be used afterwards.
	Close()
}

var _ Controller = (*Manager)(nil)
