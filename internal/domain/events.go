package domain

// Wails event names (PLAN.md §6). The UI subscribes to these; Go pushes state.
// Register every name in main.go via application.RegisterEvent.
const (
	EventConnectionState = "connection:state" // payload: ConnectionStatus
	EventChannelsTree    = "channels:tree"    // payload: ChannelNode (root)
	EventChatMessage     = "chat:message"     // payload: ChatMessage
	EventTofuMismatch    = "tofu:mismatch"    // payload: TofuPrompt
)

// Emitter pushes typed events to the UI. Implemented in main.go over
// wails application.App.Event; core depends only on this interface.
type Emitter interface {
	Emit(name string, payload any)
}
