package audio

import "sync"

// How this client treats one other person's voice: the gain the listener
// chose for them, and whether the listener silenced them locally.
//
// The two are separate facts, not one. Silencing somebody by dragging their
// gain to zero would lose the gain, so unmuting would have to invent a value -
// and 1.0 is exactly the value the listener had already decided against. They
// are kept side by side and applied together in the mix.
//
// Both are keyed by the stable certificate hash (User.Hash), so they survive
// the peer reconnecting - a new session id is the same person.
//
// Locality: this is a decision about our own speakers. Nothing here reaches
// the server, and the person on the other end is never told.

// userAudio is the treatment of one peer. Small and copied by value: the DSP
// goroutine reads one out of the map per stream per frame and must not touch
// a lock or the allocator doing it.
type userAudio struct {
	// volume is a linear gain, 1.0 = unity.
	volume float32
	// muted silences the stream entirely, keeping volume for the unmute.
	muted bool
}

// defaultUserAudio is a peer nobody has adjusted: heard, at unity.
var defaultUserAudio = userAudio{volume: 1}

// userAudioState holds one userAudio per peer.
//
// Reads run on the DSP goroutine, once per active stream per 10 ms tick, and
// take no lock and allocate nothing (a sync.Map load of a value already
// stored is neither). Writes come from binding goroutines and are rare; the
// mutex is there only to make read-modify-write atomic between them, so a
// volume change and a mute arriving together cannot lose one another.
type userAudioState struct {
	writes sync.Mutex
	values sync.Map // user hash -> userAudio
}

// get returns the treatment of one peer, or the default for a peer nobody has
// touched. Called from the DSP goroutine: no locks, no allocations.
func (s *userAudioState) get(hash string) userAudio {
	v, ok := s.values.Load(hash)
	if !ok {
		return defaultUserAudio
	}
	// A value type in an interface: the assertion copies it out, it does not
	// allocate.
	return v.(userAudio)
}

// setVolume sets the gain, leaving the mute alone. A muted peer stays muted
// and the new gain is what they come back at.
func (s *userAudioState) setVolume(hash string, volume float32) {
	s.writes.Lock()
	defer s.writes.Unlock()
	current := s.get(hash)
	current.volume = volume
	s.values.Store(hash, current)
}

// setMuted silences or restores one peer, leaving the gain alone. That is the
// whole point: unmuting restores what the listener had chosen.
func (s *userAudioState) setMuted(hash string, muted bool) {
	s.writes.Lock()
	defer s.writes.Unlock()
	current := s.get(hash)
	current.muted = muted
	s.values.Store(hash, current)
}
