package mumble

import (
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
	"github.com/LywwKkA-aD/gumble/gumble/proto/MumbleProto"
)

// The server ignores a client that sends command messages too quickly. Murmur
// runs a per-user rate limiter over them - messageburst and messagelimit,
// which default to 5 and 1/s and are what the stand and the production server
// both use - and the excess is dropped in silence: no error, no echo. The room
// keeps the old state while we draw the new one, and nothing ever corrects it.
// That is what fast clicking on the mute button looked like.
//
// So we stay inside the budget instead of finding out afterwards. Three leaves
// room for the joins and the chat messages that spend from the same allowance;
// the state is coalesced anyway, so holding a packet back costs nothing but a
// moment of the room seeing the previous state.
const (
	selfAudioBurst = 3
	// selfAudioInterval is deliberately LONGER than the server's own refill.
	//
	// Murmur allows one command message per second sustained. Sending at
	// exactly that rate leaves no margin: our packets arrive as fast as its
	// bucket refills, so the bucket hovers on empty, and a packet it drops is
	// gone for good - a written one is never retried, and if the dropped one
	// carried the last state the room keeps showing a state the user has left.
	//
	// This is reasoning about margin, not a demonstrated fix. It was reached
	// while chasing an intermittent failure of the live spam test, and it did
	// NOT turn out to be the cause: the test fails at either interval and
	// passes at either, and what it was actually short of was waiting time
	// (selfaudio_live_test.go). The margin is kept on its own merits.
	selfAudioInterval = 1500 * time.Millisecond
)

// sendBudget spaces packets the way the server expects them: a burst of
// capacity, then one per interval. It keeps the moment the next packet is
// allowed rather than a token count, so it needs no ticker of its own.
type sendBudget struct {
	// backlog is how far behind now the schedule may fall, which is what
	// makes the burst possible.
	backlog  time.Duration
	interval time.Duration
	next     time.Time
}

func newSendBudget(burst int, interval time.Duration) *sendBudget {
	if burst < 1 {
		burst = 1
	}
	return &sendBudget{
		backlog:  time.Duration(burst-1) * interval,
		interval: interval,
	}
}

// wait charges one packet and returns how long to hold it back. Zero means it
// can go now.
func (b *sendBudget) wait(now time.Time) time.Duration {
	if b.interval <= 0 {
		return 0
	}
	if earliest := now.Add(-b.backlog); b.next.Before(earliest) {
		b.next = earliest
	}
	at := b.next
	b.next = b.next.Add(b.interval)
	if !at.After(now) {
		return 0
	}
	return at.Sub(now)
}

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
// The state is written by one goroutine, never by the caller. Two callers
// racing for the socket would put the packets on the wire in an order nobody
// chose, and the server keeps whatever arrived last: a mute that lost the race
// stayed on the room's screens after the user had already switched it off.
// One writer that always sends the newest state removes both the inversion and
// the burst - twenty clicks collapse into the two or three packets that
// actually describe the outcome.
//
// The desired state is remembered so a reconnect can restore it, exactly like
// the joined channel.

// SetSelfAudio records the local microphone and monitor state and wakes the
// writer. It never blocks - core calls it while holding its own lock - and
// offline it only records the intent, applied on the next connect.
func (m *Manager) SetSelfAudio(muted, deafened bool) {
	m.mu.Lock()
	m.selfMuted, m.selfDeafened, m.hasSelfAudio = muted, deafened, true
	m.selfAudioDirty = true
	m.mu.Unlock()
	m.wakeSelfAudio()
}

// SelfAudioPending reports whether an intent of ours has still to reach the
// server. Core uses it to tell the server's own opinion apart from the echo of
// a state we have already moved on from (internal/core/selfaudio.go).
func (m *Manager) SelfAudioPending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selfAudioDirty || m.selfAudioWriting
}

// wakeSelfAudio nudges the writer. The channel holds one token: a wake that
// finds it already there is a wake the writer has not answered yet, and one
// answer covers every intent recorded before it.
func (m *Manager) wakeSelfAudio() {
	select {
	case m.selfAudioWake <- struct{}{}:
	default:
	}
}

// selfAudioLoop is the single writer. It runs for the lifetime of the Manager.
func (m *Manager) selfAudioLoop() {
	for {
		select {
		case <-m.selfAudioDone:
			return
		case <-m.selfAudioWake:
			if !m.answerWake() {
				return
			}
		}
	}
}

// answerWake serves one wake-up: hold for the server's budget if the packet
// would exceed it, then write the newest state. It returns false when the
// Manager closed while it waited.
func (m *Manager) answerWake() bool {
	if m.selfAudioWoke != nil {
		defer m.selfAudioWoke()
	}
	if !m.selfAudioReady() {
		return true
	}
	if !m.holdForBudget() {
		return false
	}
	m.flushSelfAudio()
	return true
}

// selfAudioReady reports whether there is anything to write and somewhere to
// write it. Checking before spending from the budget keeps a wake that has
// nothing behind it from pushing a real packet into the next second.
func (m *Manager) selfAudioReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selfAudioDirty && m.client != nil
}

// holdForBudget waits out the server's rate limit. It returns false when the
// Manager closed while it waited.
func (m *Manager) holdForBudget() bool {
	wait := m.selfAudioBudget.wait(time.Now())
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-m.selfAudioDone:
		return false
	case <-timer.C:
		// Whatever was clicked while we held goes out in this same packet.
		return true
	}
}

// flushSelfAudio writes the newest recorded state, not the one that woke it.
// With no session it leaves the intent standing: restoreSelfAudio publishes it
// once the next one is synced.
func (m *Manager) flushSelfAudio() {
	m.mu.Lock()
	client := m.client
	if !m.selfAudioDirty || client == nil {
		m.mu.Unlock()
		return
	}
	muted, deafened := m.selfMuted, m.selfDeafened
	m.selfAudioDirty = false
	m.selfAudioWriting = true
	m.mu.Unlock()

	m.writeSelfAudioFn(client, muted, deafened)

	m.mu.Lock()
	m.selfAudioWriting = false
	m.mu.Unlock()
}

// restoreSelfAudio re-publishes the state after a reconnect. Runs on the read
// loop inside the connect hook, next to restoreChannel - dial has not returned
// yet, so m.client is still nil and the writer cannot do this one.
func (m *Manager) restoreSelfAudio(client *gumble.Client) {
	m.mu.Lock()
	muted, deafened, ok := m.selfMuted, m.selfDeafened, m.hasSelfAudio
	m.selfAudioDirty = false
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
