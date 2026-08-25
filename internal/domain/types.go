// Package domain holds the shared model types that cross layer boundaries
// (mumble -> core -> UI). It must stay dependency-free to avoid import cycles.
package domain

import "time"

// ChannelNode is one node of the channel tree snapshot pushed to the UI.
type ChannelNode struct {
	ID       uint32        `json:"id"`
	Name     string        `json:"name"`
	Position int32         `json:"position"`
	Users    []UserInfo    `json:"users"`
	Children []ChannelNode `json:"children"`
}

// UserInfo describes one connected user inside a channel snapshot.
type UserInfo struct {
	Session   uint32 `json:"session"`        // per-connection id
	Hash      string `json:"hash,omitempty"` // stable identity: client cert fingerprint (may be empty)
	Name      string `json:"name"`
	ChannelID uint32 `json:"channelId"`
	SelfMute  bool   `json:"selfMute"`
	SelfDeaf  bool   `json:"selfDeaf"`
	IsSelf    bool   `json:"isSelf"`
}

// ChatMessage is one sanitized chat message of the current session history.
type ChatMessage struct {
	ID         string    `json:"id"`
	ChannelID  uint32    `json:"channelId"`
	Sender     string    `json:"sender"`
	SenderHash string    `json:"senderHash,omitempty"`
	HTML       string    `json:"html"` // sanitized: only b/i/u/a/br survive
	At         time.Time `json:"at"`
}

// ConnState enumerates the connection lifecycle for the UI.
type ConnState string

const (
	StateDisconnected ConnState = "disconnected"
	StateConnecting   ConnState = "connecting"
	StateConnected    ConnState = "connected"
	StateReconnecting ConnState = "reconnecting"
)

// ConnectionStatus is pushed to the UI on every lifecycle change.
type ConnectionStatus struct {
	State       ConnState `json:"state"`
	Server      string    `json:"server"`
	Error       string    `json:"error,omitempty"`
	SelfSession uint32    `json:"selfSession,omitempty"`
	SelfChannel uint32    `json:"selfChannel,omitempty"`
}

// SavedServer is one remembered server as the connect picker reads it. It
// carries whether a password is stored, never the password: the value itself
// lives in the operating system's credential store and has no reason to cross
// into the webview - a connect from the picker is made in Go.
type SavedServer struct {
	Address     string `json:"address"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
}

// SavedConnectReason names why a connect from the picker did not start. It is
// a value the UI switches on rather than a sentence it matches: the two cases
// need different screens, and matching on message text would make a reworded
// message a broken screen.
type SavedConnectReason string

const (
	// SavedConnectStarted means the connect is under way; progress arrives
	// through EventConnectionState like any other attempt.
	SavedConnectStarted SavedConnectReason = ""
	// SavedConnectUnknown means the address is not in the picker any more.
	SavedConnectUnknown SavedConnectReason = "unknown"
	// SavedConnectPassword means the stored password could not be read - a
	// locked or unavailable credential store. The UI falls back to the manual
	// form with the address filled in; typing the password always works.
	SavedConnectPassword SavedConnectReason = "password"
)

// SavedConnect is the answer to a click on the picker.
type SavedConnect struct {
	Reason SavedConnectReason `json:"reason"`
	// Address and Username are what the fallback form starts on. They are
	// echoed back rather than assumed by the UI, so the row that was clicked
	// and the form that opens cannot describe two different servers.
	Address  string `json:"address"`
	Username string `json:"username"`
	// Message is Russian and ready to render; empty when the connect started.
	Message string `json:"message"`
}

// TofuPrompt is pushed when a known server presents a new certificate.
// The UI must ask the user explicitly; AcceptFingerprint confirms.
type TofuPrompt struct {
	Server         string `json:"server"`
	OldFingerprint string `json:"oldFingerprint"`
	NewFingerprint string `json:"newFingerprint"`
}
