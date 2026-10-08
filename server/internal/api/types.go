// Package api defines the Gul broker's JSON contract for desktop clients.
package api

type LoginRequest struct {
	Username         string `json:"username"`
	Password         string `json:"password"`
	ProtocolVersion  int    `json:"protocolVersion,omitempty"`
	MemberCredential string `json:"memberCredential,omitempty"`
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

// Grant binds one media role to a logical session and channel generation.
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

// SessionToken is an opaque broker bearer, separate from LiveKit's media JWT.
type LoginResponse struct {
	SessionToken   string      `json:"sessionToken"`
	SessionID      uint32      `json:"sessionId"`
	Identity       string      `json:"identity"`
	Name           string      `json:"name"`
	ChannelID      uint32      `json:"channelId"`
	Revision       uint64      `json:"revision"`
	Grant          Grant       `json:"grant"`
	ServerID       string      `json:"serverId,omitempty"`
	Member         *MemberInfo `json:"member,omitempty"`
	CatalogVersion uint64      `json:"catalogVersion,omitempty"`
}

type State struct {
	Tree        ChannelNode `json:"tree"`
	SelfSession uint32      `json:"selfSession"`
	SelfChannel uint32      `json:"selfChannel"`
	// Roster changes do not invalidate this caller's screen grant.
	Revision       uint64      `json:"revision"`
	ServerID       string      `json:"serverId,omitempty"`
	Member         *MemberInfo `json:"member,omitempty"`
	CatalogVersion uint64      `json:"catalogVersion,omitempty"`
}

type ChannelNode struct {
	ID       uint32        `json:"id"`
	Name     string        `json:"name"`
	Position int32         `json:"position"`
	Users    []UserInfo    `json:"users"`
	Children []ChannelNode `json:"children"`
	Version  *uint64       `json:"version,omitempty"`
	Access   string        `json:"access,omitempty"`
	CanJoin  *bool         `json:"canJoin,omitempty"`
}

type UserInfo struct {
	Session   uint32 `json:"session"`
	Hash      string `json:"hash,omitempty"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	ChannelID uint32 `json:"channelId"`
	SelfMute  bool   `json:"selfMute"`
	SelfDeaf  bool   `json:"selfDeaf"`
	IsSelf    bool   `json:"isSelf"`
}

type MemberInfo struct {
	ID   *string `json:"id"`
	Role string  `json:"role"`
}
