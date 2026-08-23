package mumble

import "github.com/LywwKkA-aD/gumble/gumble"

// Self mute and deafen (PLAN.md §5). The engine already gates audio locally;
// these tell the server so other participants see the muted/deafened icons.
//
// The desired state is remembered so a reconnect can restore it, exactly like
// the joined channel. Mumble makes deafen imply mute server-side, so we send
// each flag as-is and let the server apply that rule.

// SetSelfMuted publishes whether self can transmit. It never blocks; offline
// it only records the intent, applied on the next connect.
func (m *Manager) SetSelfMuted(muted bool) {
	m.mu.Lock()
	m.selfMuted, m.hasSelfAudio = muted, true
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return
	}
	client.Do(func() {
		if client.Self != nil {
			client.Self.SetSelfMuted(muted)
		}
	})
}

// SetSelfDeafened publishes whether self can receive. Same discipline as
// SetSelfMuted.
func (m *Manager) SetSelfDeafened(deafened bool) {
	m.mu.Lock()
	m.selfDeafened, m.hasSelfAudio = deafened, true
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return
	}
	client.Do(func() {
		if client.Self != nil {
			client.Self.SetSelfDeafened(deafened)
		}
	})
}

// restoreSelfAudio re-publishes the mute and deafen state after a reconnect.
// Runs on the read loop inside the connect hook, next to restoreChannel.
func (m *Manager) restoreSelfAudio(client *gumble.Client) {
	m.mu.Lock()
	muted, deafened, ok := m.selfMuted, m.selfDeafened, m.hasSelfAudio
	m.mu.Unlock()
	if !ok || client == nil || client.Self == nil {
		return
	}
	if muted {
		client.Self.SetSelfMuted(true)
	}
	if deafened {
		client.Self.SetSelfDeafened(true)
	}
}
