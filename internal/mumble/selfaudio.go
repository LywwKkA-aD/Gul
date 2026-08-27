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

// selfAudioPair is the two flags as one value: they are decided together, sent
// together, and compared together against what the room reports.
type selfAudioPair struct{ muted, deafened bool }

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
	// A fresh intent gets its own retry: the allowance belongs to the state the
	// user asked for, not to the connection.
	m.selfAudioRetried = false
	m.mu.Unlock()
	m.wakeSelfAudio()
}

// selfAudioEchoWait bounds how long a written pair waits to be echoed.
//
// It has to outlast a round trip on a bad link, and it has to end: the server
// drops a command message it considers too fast without a word, and a client
// that waited forever for that echo would ignore the room for the rest of the
// session - including an admin muting it.
const selfAudioEchoWait = 4 * time.Second

// SelfAudioSettled reports whether the pair a channel tree carries can be taken
// as the server's own opinion.
//
// It cannot while an intent of ours is unwritten, and - this is the part that
// is easy to get wrong - it cannot for a while after the packet has been
// written either. Handing a packet to the socket takes microseconds; the room's
// answer comes back a round trip later, and in between any event from anybody
// else makes the server send a tree that still carries OUR previous flags,
// because gumble learns ours only from the echo. Trusting such a tree adopts
// the state the user has just left, and adoption deliberately does not write
// back, so nothing corrects it afterwards.
//
// So a written pair is awaited: a tree that carries it settles the wait, a tree
// that carries anything else is older than our write and is refused, and the
// wait expires on its own so a dropped packet cannot silence the room forever.
func (m *Manager) SelfAudioSettled(muted, deafened bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.selfAudioDirty || m.selfAudioWriting {
		return false
	}
	if !m.selfAudioAwaiting {
		return true
	}
	if muted == m.selfAudioSent.muted && deafened == m.selfAudioSent.deafened {
		m.selfAudioAwaiting = false
		m.selfAudioRetried = false
		return true
	}
	if time.Since(m.selfAudioSentAt) < selfAudioEchoWait {
		return false
	}

	// The wait is over and the room still shows something else, so our packet
	// never arrived. The server drops a command message it considers too fast
	// without a word, and other command messages - joining a channel, sending
	// chat - spend from the same allowance, so staying inside our own budget is
	// not a guarantee.
	//
	// Send it once more rather than adopting straight away. Adopting here is
	// what "I pressed it and nothing happened" is made of: the click is
	// discarded in silence and the button springs back. One retry heals the
	// ordinary case - a single lost packet - and stops there, because a server
	// that keeps refusing is a server that means it, and arguing with it
	// forever would be worse than losing the click.
	m.selfAudioAwaiting = false
	if m.selfAudioRetried {
		m.selfAudioRetried = false
		return true
	}
	m.selfAudioRetried = true
	m.selfAudioDirty = true
	go m.wakeSelfAudio()
	return false
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
	// From here the pair is on the wire and not yet acknowledged. Until the
	// room echoes it, a tree saying anything else is older than this write
	// (SelfAudioSettled).
	m.selfAudioSent = selfAudioPair{muted: muted, deafened: deafened}
	m.selfAudioSentAt = time.Now()
	m.selfAudioAwaiting = true
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

	// This packet waits for its echo exactly like any other. The first trees of
	// a fresh session arrive while it is still in flight, and they show the
	// unmuted state the session started in.
	m.mu.Lock()
	m.selfAudioSent = selfAudioPair{muted: muted, deafened: deafened}
	m.selfAudioSentAt = time.Now()
	m.selfAudioAwaiting = true
	m.mu.Unlock()
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

// selfAudioPending reports whether an intent has still to reach the socket. It
// is the first half of SelfAudioSettled and exists on its own so a test can
// wait for the writer to drain without also waiting for a room to answer.
func (m *Manager) selfAudioPending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selfAudioDirty || m.selfAudioWriting
}
