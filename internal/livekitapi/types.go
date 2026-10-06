// Package livekitapi defines the wire contract of the local Gul application
// broker. It contains no HTTP clients, server state, or LiveKit SDK types.
package livekitapi

import "github.com/LywwKkA-aD/Gul/internal/domain"

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ChannelRequest struct {
	ChannelID uint32 `json:"channelId"`
}

type AudioState struct {
	Muted    bool `json:"muted"`
	Deafened bool `json:"deafened"`
}

type ScreenRequest struct {
	ChannelID uint32 `json:"channelId"`
	Revision  uint64 `json:"revision"`
}

// Grant is specific to one role, logical session and channel generation.
// Only a screen Grant may cross from native Go into the webview.
type Grant struct {
	URL           string `json:"url"`
	Token         string `json:"token"`
	Identity      string `json:"identity"`
	Room          string `json:"room"`
	OwnerIdentity string `json:"ownerIdentity"`
	SessionID     uint32 `json:"sessionId"`
	ChannelID     uint32 `json:"channelId"`
	Revision      uint64 `json:"revision"`
}

// LoginResponse is also returned on a channel switch. SessionToken remains in
// native Go; it is an opaque local broker credential, not a LiveKit JWT.
type LoginResponse struct {
	SessionToken string `json:"sessionToken"`
	SessionID    uint32 `json:"sessionId"`
	Identity     string `json:"identity"`
	Name         string `json:"name"`
	ChannelID    uint32 `json:"channelId"`
	Revision     uint64 `json:"revision"`
	Grant        Grant  `json:"grant"`
}

type State struct {
	Tree        domain.ChannelNode `json:"tree"`
	SelfSession uint32             `json:"selfSession"`
	SelfChannel uint32             `json:"selfChannel"`
	// Revision changes only when this caller changes channels. Other users'
	// roster/audio changes must not invalidate this caller's screen grant.
	Revision uint64 `json:"revision"`
}
