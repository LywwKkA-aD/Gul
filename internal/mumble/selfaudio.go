package mumble

import (
	"github.com/LywwKkA-aD/gumble/gumble"
	"github.com/LywwKkA-aD/gumble/gumble/proto/MumbleProto"
)

// Self mute and deafen (PLAN.md §5). The engine already gates audio locally;
// these tell the server so other participants see the muted and deafened
// icons.
//
// Both flags always travel in ONE UserState. Murmur forces self_mute on
// alongside self_deaf and does NOT clear it when self_deaf goes false
// (verified live against v1.5.915: after a deafen/undeafen cycle sent as two
// separate flag updates, the room saw mute=true forever and the user was
// inaudible with no working control). Publishing the pair states our whole
// intent every time, so the server has nothing left to infer.
//
// The desired state is remembered so a reconnect can restore it, exactly like
// the joined channel.

// SetSelfAudio publishes the local microphone and monitor state. It never
// blocks; offline it only records the intent, applied on the next connect.
func (m *Manager) SetSelfAudio(muted, deafened bool) {
	m.mu.Lock()
	m.selfMuted, m.selfDeafened, m.hasSelfAudio = muted, deafened, true
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return
	}
	client.Do(func() { writeSelfAudio(client, muted, deafened) })
}

// restoreSelfAudio re-publishes the state after a reconnect. Runs on the read
// loop inside the connect hook, next to restoreChannel.
func (m *Manager) restoreSelfAudio(client *gumble.Client) {
	m.mu.Lock()
	muted, deafened, ok := m.selfMuted, m.selfDeafened, m.hasSelfAudio
	m.mu.Unlock()
	if !ok || (!muted && !deafened) {
		// A fresh session starts unmuted and undeafened; saying so again
		// would be one pointless packet per reconnect.
		return
	}
	writeSelfAudio(client, muted, deafened)
}

// writeSelfAudio sends the pair. Caller runs on the read loop or inside
// Client.Do, where Self is stable.
func writeSelfAudio(client *gumble.Client, muted, deafened bool) {
	if client == nil || client.Self == nil {
		return
	}
	session := client.Self.Session
	_ = client.Conn.WriteProto(&MumbleProto.UserState{
		Session:  &session,
		SelfMute: &muted,
		SelfDeaf: &deafened,
	})
}
