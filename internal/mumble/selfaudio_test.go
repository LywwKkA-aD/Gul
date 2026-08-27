package mumble

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
)

// selfAudioRecorder captures the writes and can hold the first one open, which
// is the only way to be inside the window the bug lived in.
type selfAudioRecorder struct {
	mu      sync.Mutex
	writes  []selfAudioPair
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *selfAudioRecorder) write(_ *gumble.Client, muted, deafened bool) {
	r.once.Do(func() {
		close(r.started)
		<-r.release
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, selfAudioPair{muted, deafened})
}

func (r *selfAudioRecorder) snapshot() []selfAudioPair {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]selfAudioPair(nil), r.writes...)
}

func newSelfAudioManager(t *testing.T) (*Manager, *selfAudioRecorder) {
	t.Helper()
	m, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), Callbacks{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
	rec := &selfAudioRecorder{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.writeSelfAudioFn = rec.write
	return m, rec
}

// waitFor polls until cond holds, the way the live helpers do.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Spamming the buttons must end on the state of the last click, and must not
// put one packet per click on the wire.
//
// This is the bug the users hit: two callers wrote to the socket themselves, so
// the packets arrived in an order nobody chose and the server kept whichever
// landed last - a mute that lost the race stayed on the room's screens after
// the user had switched it off. One writer that always sends the newest state
// has nothing left to invert.
func TestSelfAudioWriterSendsTheLatestIntentAndCoalesces(t *testing.T) {
	t.Parallel()
	m, rec := newSelfAudioManager(t)
	m.mu.Lock()
	m.client = &gumble.Client{}
	m.mu.Unlock()

	m.SetSelfAudio(true, false)
	<-rec.started // the writer is busy and cannot answer for a while

	// Twenty clicks land while it is busy. Recording an intent must never
	// block on the socket - core holds its own lock across this call - so a
	// loop that hangs here is itself the failure.
	for i := range 20 {
		m.SetSelfAudio(i%2 == 0, false)
	}
	m.SetSelfAudio(false, false)
	if !m.selfAudioPending() {
		t.Fatal("selfAudioPending() is false with an unwritten intent")
	}
	close(rec.release)

	waitFor(t, "the writer to drain", func() bool { return !m.selfAudioPending() })

	writes := rec.snapshot()
	if len(writes) == 0 {
		t.Fatal("nothing was written")
	}
	if got := writes[len(writes)-1]; got != (selfAudioPair{false, false}) {
		t.Fatalf("last packet = %+v, want the last click {false false}", got)
	}
	if len(writes) > 3 {
		t.Fatalf("22 clicks produced %d packets, want them coalesced", len(writes))
	}
}

// An intent recorded with no session waits for one instead of being dropped:
// the writer has nowhere to put it, and the button must not need a second
// press once the connection is up.
func TestSelfAudioIntentSurvivesUntilThereIsASession(t *testing.T) {
	t.Parallel()
	m, rec := newSelfAudioManager(t)
	close(rec.release) // nothing to hold open here
	flushed := make(chan struct{}, 4)
	m.selfAudioWoke = func() { flushed <- struct{}{} }

	m.SetSelfAudio(true, true)
	// Wait for the writer to answer the wake and find no session, so the
	// session below is genuinely the thing that has to revive the intent.
	<-flushed
	if !m.selfAudioPending() {
		t.Fatal("the offline intent was dropped")
	}

	m.setSession(&Session{client: &gumble.Client{}})
	waitFor(t, "the writer to pick the session up", func() bool { return !m.selfAudioPending() })

	writes := rec.snapshot()
	if len(writes) != 1 || writes[0] != (selfAudioPair{true, true}) {
		t.Fatalf("writes = %+v, want one {true true}", writes)
	}
}

// Close must stop the writer; a second Close must not panic on the channel.
func TestSelfAudioWriterStopsWithTheManager(t *testing.T) {
	t.Parallel()
	m, rec := newSelfAudioManager(t)
	close(rec.release)
	m.Close()
	m.Close()

	m.SetSelfAudio(true, false)
	time.Sleep(50 * time.Millisecond)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("writes after Close = %+v, want none", got)
	}
}

// The budget is what keeps our packets out of the server's bin: murmur ignores
// a client that sends command messages faster than messageburst/messagelimit
// allow, silently, so the room would keep a state we had already left.
func TestSendBudgetAllowsABurstThenOnePerInterval(t *testing.T) {
	t.Parallel()
	const interval = time.Second
	b := newSendBudget(3, interval)
	now := time.Unix(1_700_000_000, 0)

	for i := range 3 {
		if got := b.wait(now); got != 0 {
			t.Fatalf("packet %d of the burst waited %v, want none", i, got)
		}
	}
	if got := b.wait(now); got != interval {
		t.Fatalf("the packet after the burst waited %v, want %v", got, interval)
	}
	if got := b.wait(now); got != 2*interval {
		t.Fatalf("the next one waited %v, want %v", got, 2*interval)
	}

	// Idling refills the allowance, and never past the burst.
	b = newSendBudget(3, interval)
	for range 3 {
		b.wait(now)
	}
	now = now.Add(time.Minute)
	for i := range 3 {
		if got := b.wait(now); got != 0 {
			t.Fatalf("packet %d after idling waited %v, want none", i, got)
		}
	}
	if got := b.wait(now); got != interval {
		t.Fatalf("the fourth after idling waited %v, want %v", got, interval)
	}
}

// A budget nobody configured must not hold anything back.
func TestSendBudgetWithoutAnIntervalNeverWaits(t *testing.T) {
	t.Parallel()
	b := newSendBudget(3, 0)
	now := time.Unix(1_700_000_000, 0)
	for range 10 {
		if got := b.wait(now); got != 0 {
			t.Fatalf("wait = %v, want none when the budget is off", got)
		}
	}
}

// The writer asks the budget before every packet. With an allowance of one
// the second click cannot go out at all, and it has to stay pending rather
// than be dropped: the server would otherwise keep a state the user has left.
//
// The count is asserted the safe way round - a slow machine gives a wrong
// packet more time to appear, not less - because the spacing itself is
// already pinned deterministically by TestSendBudget...
func TestSelfAudioWriterWaitsForTheServersBudget(t *testing.T) {
	t.Parallel()
	m, rec := newSelfAudioManager(t)
	close(rec.release) // nothing to hold open here
	m.selfAudioBudget = newSendBudget(1, time.Hour)
	m.mu.Lock()
	m.client = &gumble.Client{}
	m.mu.Unlock()

	m.SetSelfAudio(true, false)
	waitFor(t, "the first packet", func() bool { return len(rec.snapshot()) == 1 })

	m.SetSelfAudio(false, false)
	time.Sleep(100 * time.Millisecond)

	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("packets = %+v, want only the one the budget allowed", got)
	}
	if !m.selfAudioPending() {
		t.Fatal("the click held back by the budget was forgotten")
	}
}

// A written packet is not a settled state, and treating it as one silently
// undoes the click.
//
// The packet reaches the socket in microseconds; the room's answer comes back a
// round trip later. In between, anything anybody else does - a join, a leave,
// somebody else's mute - makes the server send a tree, and that tree still
// carries OUR old flags, because gumble learns ours only from the echo
// (snapshot.go reads Self.SelfMuted). Core asks this package whether such a
// tree can be trusted; answering yes there adopts the state the user just left,
// and adoption deliberately does not write back, so nothing corrects it.
//
// That is what "the icon sometimes stays" was.
func TestSelfAudioIsNotSettledUntilTheRoomEchoesItBack(t *testing.T) {
	t.Parallel()
	m, rec := newSelfAudioManager(t)
	close(rec.release) // the writer never blocks in this test
	m.mu.Lock()
	m.client = &gumble.Client{}
	m.mu.Unlock()

	m.SetSelfAudio(true, true)
	waitFor(t, "the packet to reach the socket", func() bool {
		return len(rec.snapshot()) == 1
	})

	// The packet is on the wire and nothing of ours is unwritten, but the room
	// has not answered yet.
	if m.SelfAudioSettled(false, false) {
		t.Fatal("a tree carrying the state before the gesture was called settled")
	}
	// The same is true of any other pair that is not what we sent.
	if m.SelfAudioSettled(true, false) {
		t.Fatal("a tree carrying a state we never asked for was called settled")
	}
	// The echo of our own pair settles it, and everything after it is the
	// server speaking for itself again - an admin mute has to get through.
	if !m.SelfAudioSettled(true, true) {
		t.Fatal("the echo of our own pair was not accepted")
	}
	if !m.SelfAudioSettled(false, false) {
		t.Fatal("the server lost its authority after the echo had settled")
	}
}

// A packet the server never answers is sent once more before the room wins.
//
// Adopting straight away is what "I pressed it and nothing happened" is made
// of: the click is discarded in silence and the button springs back. Murmur
// drops a command message it considers too fast without a word, and joins and
// chat messages spend from the same allowance, so our own budget is not a
// guarantee. One retry heals the ordinary case and stops there - a server that
// keeps refusing means it, and arguing forever would be worse than losing one
// click.
func TestAnUnansweredPairIsSentAgainBeforeTheRoomWins(t *testing.T) {
	t.Parallel()
	m, rec := newSelfAudioManager(t)
	close(rec.release)
	m.mu.Lock()
	m.client = &gumble.Client{}
	m.mu.Unlock()

	m.SetSelfAudio(true, true)
	waitFor(t, "the packet to reach the socket", func() bool {
		return len(rec.snapshot()) == 1
	})
	if m.SelfAudioSettled(false, false) {
		t.Fatal("the disagreeing tree was accepted immediately")
	}

	// Nothing came back. The wait runs out.
	m.mu.Lock()
	m.selfAudioSentAt = m.selfAudioSentAt.Add(-2 * selfAudioEchoWait)
	m.mu.Unlock()

	if m.SelfAudioSettled(false, false) {
		t.Fatal("the room won on the first silence, discarding the click without a second try")
	}
	waitFor(t, "the intent to be sent again", func() bool {
		return len(rec.snapshot()) == 2
	})
	if got := rec.snapshot()[1]; got != (selfAudioPair{true, true}) {
		t.Fatalf("the retry carried %+v, want the intent {true true}", got)
	}

	// Still nothing. Now the room is the authority, or a packet the server
	// really refuses would leave it unheard for the rest of the session.
	m.mu.Lock()
	m.selfAudioSentAt = m.selfAudioSentAt.Add(-2 * selfAudioEchoWait)
	m.mu.Unlock()

	if !m.SelfAudioSettled(false, false) {
		t.Fatal("the wait never ends: the room can no longer be heard at all")
	}
}
