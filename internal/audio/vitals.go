package audio

import "log/slog"

// voiceVitalsTicks is how often the receive panel is written, in 10 ms DSP
// ticks. It matches the connection panel's five seconds (mumble/manager.go)
// so that the two readings line up in an archive instead of having to be
// interpolated against each other.
const voiceVitalsTicks = 500

// VoiceVitals is one reading of what the receive path actually did with the
// audio it was handed.
//
// It exists for the same reason the connection panel does (mumble/vitals.go),
// and it was written after that one proved the point: a user reports that
// speech breaks up, and every number the client keeps describes the socket.
// The connection can be perfectly healthy while the sound is not - a burst
// that outran the ring, a stream running dry every second, a buffer that has
// quietly grown to half a second of latency to survive the link. None of that
// touches a byte counter, and none of it was written down anywhere.
//
// The distinction the fields are built around is fault versus choice. Audio
// can leave this path in four ways and they mean four different things:
// concealed (it never arrived), late (it arrived after its turn), overflow
// (it arrived in a burst too big to hold), trimmed (we dropped it on purpose
// to give latency back). Collapsing them into one "loss" number is what makes
// an incident report unusable, so they are counted apart.
//
// Nothing here can name a person or a server: every field is a count of
// frames, and the panel travels in diagnostics archives.
type VoiceVitals struct {
	JitterCounts
	// Streams is how many senders were live when the reading was taken.
	Streams int
	// Depth is the deepest current target across those senders, in frames.
	// It is the latency being paid right now, and it is the number that says
	// a link has been bursting: the buffer only grows when it runs dry.
	Depth int
}

// JitterCounts is what the buffers did, summed over every sender this session
// has heard - including the ones whose streams have since been released.
//
// Every field is monotonic for the life of the reading it comes from. They are
// counted rather than sampled because the reading is always taken after the
// trouble: a stall lasting two seconds is invisible to a poll every five.
type JitterCounts struct {
	// Played is slots filled with audio that really arrived.
	Played uint64
	// Concealed is slots the decoder had to invent. This is the one a
	// listener would recognise - it is the gap they heard.
	Concealed uint64
	// Late is frames that arrived after their slot had already gone. The
	// audio existed; the link was too slow to deliver it in time. Concealment
	// with lateness is a slow link, concealment without it is a lossy one.
	Late uint64
	// Duplicate is second copies, which point at the sender or the repacking
	// rather than at the link.
	Duplicate uint64
	// Overflow is frames given up because a burst was bigger than the ring
	// can hold. This is the design limit being exceeded, not a hiccup.
	Overflow uint64
	// Trimmed is frames dropped deliberately to hand back latency the buffer
	// had accumulated. It is the only entry here that is not a fault.
	Trimmed uint64
	// Stalls is how many times a stream ran dry in the middle of a phrase,
	// which is the event that deepens the target.
	Stalls uint64
}

// add accumulates another reading. The panel sums buffers that were never
// alive at the same time, so plain addition is the whole operation.
func (c *JitterCounts) add(other JitterCounts) {
	c.Played += other.Played
	c.Concealed += other.Concealed
	c.Late += other.Late
	c.Duplicate += other.Duplicate
	c.Overflow += other.Overflow
	c.Trimmed += other.Trimmed
	c.Stalls += other.Stalls
}

// LogValue renders the panel as one group, in the order a reader needs it:
// how many senders, how much latency, then what became of their audio.
//
// Depth is converted to milliseconds because that is what the number is about.
// A frame count is an implementation detail of the ring; the latency a
// listener is paying is the thing worth reading off a log.
func (v VoiceVitals) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("streams", v.Streams),
		slog.Int("depth_ms", v.Depth*FrameMs),
		slog.Uint64("played", v.Played),
		slog.Uint64("concealed", v.Concealed),
		slog.Uint64("late", v.Late),
		slog.Uint64("duplicate", v.Duplicate),
		slog.Uint64("overflow", v.Overflow),
		slog.Uint64("trimmed", v.Trimmed),
		slog.Uint64("stalls", v.Stalls),
	)
}
