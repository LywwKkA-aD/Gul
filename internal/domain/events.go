package domain

// Wails event names (PLAN.md §6). The UI subscribes to these; Go pushes state.
// Register every name in main.go via application.RegisterEvent.
const (
	EventConnectionState   = "connection:state"   // payload: ConnectionStatus
	EventConnectionLatency = "connection:latency" // payload: ConnectionLatency
	EventChannelsTree      = "channels:tree"      // payload: ChannelNode (root)
	EventChatMessage       = "chat:message"       // payload: ChatMessage
	EventTofuMismatch      = "tofu:mismatch"      // payload: TofuPrompt
	EventUserTalking       = "user:talking"       // payload: TalkingEvent
	EventAudioLevels       = "audio:levels"       // payload: AudioLevels
)

// ConnectionLatency is the smoothed TCP round-trip time of the active Mumble
// session. Zero is valid on a local server; no event means no sample yet.
type ConnectionLatency struct {
	PingMS float64 `json:"pingMs"`
}

// TalkingEvent reports a remote user starting or stopping to speak.
type TalkingEvent struct {
	Session uint32 `json:"session"`
	Hash    string `json:"hash,omitempty"`
	Talking bool   `json:"talking"`
}

// AudioLevels carries mic and output meters in dBFS (about every 50 ms
// while the voice engine runs).
type AudioLevels struct {
	MicDb float64 `json:"micDb"`
	OutDb float64 `json:"outDb"`
}

// AudioDevice describes one selectable audio device. ID is an opaque
// hex-encoded backend identifier; empty means the system default.
type AudioDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// Emitter pushes typed events to the UI. Implemented in main.go over
// wails application.App.Event; core depends only on this interface.
type Emitter interface {
	Emit(name string, payload any)
}
