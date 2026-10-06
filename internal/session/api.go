// Package session defines transport-independent boundaries between the client
// core, its media transport and the native audio engine.
package session

import (
	"strings"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

type RawMessage struct {
	ChannelID  uint32
	Sender     string
	SenderHash string
	HTML       string
}

type Callbacks struct {
	OnStatus    func(domain.ConnectionStatus)
	OnLatency   func(domain.ConnectionLatency)
	OnTree      func(domain.ChannelNode)
	OnMessage   func(RawMessage)
	OnTofu      func(domain.TofuPrompt)
	OnTransport func(address, transport string)
}

// Controller methods preserve the service boundary while the media transport
// changes. Callbacks must never run synchronously under a core-owned lock.
type Controller interface {
	Connect(address, username, password string)
	Disconnect()
	Join(channelID uint32) error
	SendMessage(channelID uint32, text string) error
	SetSelfAudio(muted, deafened bool)
	SelfAudioSettled(muted, deafened bool) bool
	// Legacy hooks are no-ops for transports using ordinary authenticated TLS.
	PreferTransport(address, transport string)
	AcceptFingerprint()
	Status() domain.ConnectionStatus
	Close()
}

// VoicePacket contains ordered Opus, with Sequence measured on the engine's
// 48 kHz / 10 ms grid. RTP reordering belongs to the transport, before decoding.
// Session identifies a media stream; Key identifies its owner for gain/mute.
type VoicePacket struct {
	Session  uint32
	Key      string
	Sequence int64
	Opus     []byte
	Final    bool
	// LostFrames requests bounded 10 ms PLC before the next ordered Opus packet.
	LostFrames int
	// Reset releases decoder and queued PCM state when the media epoch changes.
	Reset bool
}

func PeerKeyIsMortal(key string) bool { return strings.HasPrefix(key, "s:") }
