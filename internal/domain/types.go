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

// TofuPrompt is pushed when a known server presents a new certificate.
// The UI must ask the user explicitly; AcceptFingerprint confirms.
type TofuPrompt struct {
	Server         string `json:"server"`
	OldFingerprint string `json:"oldFingerprint"`
	NewFingerprint string `json:"newFingerprint"`
}
