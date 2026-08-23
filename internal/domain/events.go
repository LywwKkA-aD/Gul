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
	EventAudioSelf         = "audio:self"         // payload: SelfAudioState
	EventAudioPTT          = "audio:ptt"          // payload: PTTState
	EventAudioSelfTalking  = "audio:selftalking"  // payload: SelfTalkingState
)

// SelfAudioState is the local microphone and monitor state. Mute and deafen
// are reachable from the window and from the system tray, so core pushes this
// on every change whichever path made it, and the UI renders what it is told
// rather than what it last asked for. A request that changes nothing pushes
// nothing.
type SelfAudioState struct {
	Muted    bool `json:"muted"`
	Deafened bool `json:"deafened"`
}

// PTTState reports whether push-to-talk is currently transmitting. The window
// listener sets this directly, but the global key (which fires with the window
// unfocused) can only reach the UI through this event, so the microphone
// indicator tracks a global key the same as a focused one. Deduplicated: no
// event when the held state does not change.
type PTTState struct {
	Held bool `json:"held"`
}

// SelfTalkingState reports whether our own microphone is transmitting right
// now. Our voice never returns from the server, so the local speaking
// indication has no other source: without this the user is the only
// participant whose halo never lights up.
type SelfTalkingState struct {
	Talking bool `json:"talking"`
}

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
