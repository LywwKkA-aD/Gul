package audio

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
)

// vitalsRigSession is the sender the rig starts with.
const vitalsRigSession = 7

// newVitalsRig builds a receive pipeline holding one live sender, which is the
// smallest arrangement in which a reading means anything.
func newVitalsRig(t *testing.T) (*rxPipeline, FrameSink) {
	t.Helper()
	chain, err := newDSPChain(DSPOptions{}, slog.Default())
	if err != nil {
		t.Fatalf("newDSPChain: %v", err)
	}
	t.Cleanup(chain.close)

	rx := newRxPipeline(Config{Log: slog.Default()}, chain)
	t.Cleanup(rx.close)
	rx.streams[vitalsRigSession] = newVitalsStream(t, vitalsRigSession, "sender")
	return rx, &collectSink{}
}

func newVitalsStream(t *testing.T, session uint32, key string) *rxStream {
	t.Helper()
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	return &rxStream{
		session:    session,
		key:        key,
		dec:        dec,
		jit:        NewJitter(),
		pcm:        make([]int16, opus.MaxFrameSize),
		lastPacket: time.Now(),
	}
}

// The two numbers a listener would recognise: how much real audio they were
// given, and how much of what they heard the decoder had to invent.
//
// Concealment is the whole point of the panel. "It breaks up" is the complaint
// every voice client gets and the one nothing here could answer, because the
// only place a gap was visible was the sound itself.
func TestTheBufferCountsRealAudioApartFromInventedAudio(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	// Eight frames to prime with, and a ninth beyond a deliberate hole so the
	// missing slot sits inside the range the sender announced.
	jitterPrime(j, 0, jitterStartFrames)
	j.Push(jitterStartFrames+1, jitterFrameFor(jitterStartFrames+1))

	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	if got := j.Pop(dst); got != JitterConceal {
		t.Fatalf("the hole gave %s, want JitterConceal", jitterResultName(got))
	}

	counts := j.Counts()
	if counts.Played != jitterStartFrames {
		t.Errorf("played = %d, want %d", counts.Played, jitterStartFrames)
	}
	if counts.Concealed != 1 {
		t.Errorf("concealed = %d, want 1", counts.Concealed)
	}
}

// A frame that arrives after its slot has gone is the reading that separates a
// slow link from a lossy one: the audio existed, it simply came too late to be
// played. Without the count both look identical - as concealment.
func TestALateFrameIsCountedRatherThanQuietlyDropped(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < 3; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}

	j.Push(1, jitterFrameFor(1)) // already played and gone
	if got := j.Counts().Late; got != 1 {
		t.Errorf("late = %d, want 1", got)
	}
}

// A second copy of a frame is not a fault of ours, but it is worth telling
// apart from a late one: duplicates point at the sender or the repacking,
// lateness points at the link.
func TestADuplicateIsCountedAsItsOwnThing(t *testing.T) {
	t.Parallel()
	j := NewJitter()

	jitterPrime(j, 0, jitterStartFrames)
	j.Push(3, jitterFrameFor(3)) // slot 3 is still queued

	counts := j.Counts()
	if counts.Duplicate != 1 {
		t.Errorf("duplicate = %d, want 1", counts.Duplicate)
	}
	if counts.Late != 0 {
		t.Errorf("late = %d, want 0: a duplicate is not a late frame", counts.Late)
	}
}

// A burst too big for the ring costs audio, and the cost has to be visible.
// This is the TCP head-of-line case the buffer was built for, and the only
// sign of it going past the design limit is this counter.
func TestABurstBiggerThanTheRingReportsWhatItCost(t *testing.T) {
	t.Parallel()
	j := NewJitter()

	j.Push(0, jitterFrameFor(0))
	// One past the far edge of the ring: the oldest frame cannot be kept.
	j.Push(jitterRingFrames, jitterFrameFor(jitterRingFrames))

	if got := j.Counts().Overflow; got != 1 {
		t.Errorf("overflow = %d, want 1", got)
	}
}

// Latency shed on purpose must not read as latency lost. Both remove a frame
// from the queue; only one of them is a fault.
func TestATrimIsCountedAsOurOwnDoing(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	const burst = 90 // far above the ceiling, so trimming is forced
	jitterPrime(j, 0, burst)
	for i := 0; i < 40; i++ {
		if got := j.Pop(dst); got != JitterFrame {
			t.Fatalf("tick %d: Pop = %s, want JitterFrame", i, jitterResultName(got))
		}
	}

	counts := j.Counts()
	if counts.Trimmed == 0 {
		t.Fatal("the queue was clawed back from 90 frames and nothing was counted as trimmed")
	}
	if counts.Overflow != 0 {
		t.Errorf("overflow = %d, want 0: trimming is not the ring overflowing", counts.Overflow)
	}
	if counts.Concealed != 0 {
		t.Errorf("concealed = %d, want 0: trimming must never be heard as a gap", counts.Concealed)
	}
}

// Running dry mid-transmission is the event the adaptive depth exists for.
// Counting it is what turns "the buffer is deep today" into a reason.
func TestRunningDryMidPhraseIsCountedAsAStall(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	if got := j.Pop(dst); got != JitterConceal {
		t.Fatalf("the dry tick gave %s, want JitterConceal", jitterResultName(got))
	}

	if got := j.Counts().Stalls; got != 1 {
		t.Errorf("stalls = %d, want 1", got)
	}
}

// The counters describe the buffer, not the phrase. A talk spurt that ends -
// and a Reset for a peer who reconnected - must not take the evidence with it,
// because the reading is always taken after the trouble, never during.
func TestTheCountersOutliveTheTalkSpurtTheyDescribe(t *testing.T) {
	t.Parallel()
	j := NewJitter()
	dst := make([]int16, FrameSamples)

	jitterPrime(j, 0, jitterStartFrames)
	for seq := int64(0); seq < jitterStartFrames; seq++ {
		jitterPopFrame(t, j, dst, "spurt", seq)
	}
	played := j.Counts().Played
	if played == 0 {
		t.Fatal("nothing was counted as played")
	}

	j.PushFinal(jitterStartFrames - 1) // the phrase ends cleanly
	j.Pop(dst)
	if got := j.Counts().Played; got != played {
		t.Errorf("played = %d after the phrase ended, want %d kept", got, played)
	}

	j.Reset()
	if got := j.Counts().Played; got != played {
		t.Errorf("played = %d after Reset, want %d kept", got, played)
	}
}

// Aggregation has to be plain addition, because the panel sums buffers that
// were never alive at the same time.
func TestCountsAddUp(t *testing.T) {
	t.Parallel()
	total := JitterCounts{Played: 1, Concealed: 2, Late: 3, Duplicate: 4, Overflow: 5, Trimmed: 6, Stalls: 7}
	total.add(JitterCounts{Played: 10, Concealed: 20, Late: 30, Duplicate: 40, Overflow: 50, Trimmed: 60, Stalls: 70})

	want := JitterCounts{Played: 11, Concealed: 22, Late: 33, Duplicate: 44, Overflow: 55, Trimmed: 66, Stalls: 77}
	if total != want {
		t.Errorf("add gave %+v, want %+v", total, want)
	}
}

// The panel has to say something even when nothing is wrong, because "quiet"
// is itself a reading: a session with no concealment did not have an audio
// problem, and that is worth being able to prove.
func TestTheVitalsLineNamesEveryReading(t *testing.T) {
	t.Parallel()
	v := VoiceVitals{
		JitterCounts: JitterCounts{
			Played: 100, Concealed: 5, Late: 4, Duplicate: 3,
			Overflow: 2, Trimmed: 1, Stalls: 6,
		},
		Streams: 2,
		Depth:   12,
	}

	value := v.LogValue()
	if value.Kind() != slog.KindGroup {
		t.Fatalf("LogValue kind = %v, want a group", value.Kind())
	}
	got := make(map[string]string)
	for _, attr := range value.Group() {
		got[attr.Key] = attr.Value.String()
	}
	for key, want := range map[string]string{
		"streams": "2", "depth_ms": "120", "played": "100", "concealed": "5",
		"late": "4", "duplicate": "3", "overflow": "2", "trimmed": "1", "stalls": "6",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
}

// Depth is reported in milliseconds because that is the unit the number is
// about: it is latency the listener is paying, not an implementation detail of
// the ring.
func TestDepthIsReportedAsLatencyNotAsFrames(t *testing.T) {
	t.Parallel()
	v := VoiceVitals{Depth: jitterStartFrames}
	for _, attr := range v.LogValue().Group() {
		if attr.Key == "depth_ms" {
			if got := attr.Value.Int64(); got != JitterStartMs {
				t.Fatalf("depth_ms = %d, want %d", got, JitterStartMs)
			}
			return
		}
	}
	t.Fatal("the panel never reported a depth")
}

// The reading has to outlive the stream it came from.
//
// This is the trap the panel exists to avoid. Per-sender state is released
// three seconds after a peer stops talking, and a naive sum over live streams
// would therefore report a clean session to anyone who asked after the room
// went quiet - which is exactly when somebody asks. A user who says "it broke
// up ten minutes ago" would be answered with zeros.
func TestTheReadingOutlivesTheStreamItCameFrom(t *testing.T) {
	t.Parallel()
	rx, sink := newVitalsRig(t)

	s := rx.streams[vitalsRigSession]
	dst := make([]int16, FrameSamples)
	jitterPrime(s.jit, 0, jitterStartFrames)
	for range jitterStartFrames {
		s.jit.Pop(dst)
	}
	s.jit.Pop(dst) // one dry tick, so there is a stall to remember too

	before := rx.vitals()
	if before.Played == 0 || before.Stalls == 0 {
		t.Fatalf("nothing was counted before the stream was released: %+v", before)
	}
	if before.Streams != 1 {
		t.Fatalf("streams = %d while one sender was live, want 1", before.Streams)
	}

	// The sender stops talking for longer than the idle timeout, which is
	// what releases the decoder and the buffer.
	s.jit.Reset()
	s.lastPacket = time.Now().Add(-2 * streamIdleTimeout)
	rx.tick(sink, false, &userAudioState{}, 0)
	if len(rx.streams) != 0 {
		t.Fatalf("the stream was not released; this test proves nothing")
	}

	after := rx.vitals()
	if after.Streams != 0 {
		t.Errorf("streams = %d after the room went quiet, want 0", after.Streams)
	}
	if after.Played != before.Played {
		t.Errorf("played = %d after the stream was released, want %d kept", after.Played, before.Played)
	}
	if after.Stalls != before.Stalls {
		t.Errorf("stalls = %d after the stream was released, want %d kept", after.Stalls, before.Stalls)
	}
}

// Closing the pipeline is the other way a stream disappears, and it happens at
// exactly the moment a reading matters most: the session ending.
func TestClosingThePipelineKeepsWhatItMeasured(t *testing.T) {
	t.Parallel()
	rx, _ := newVitalsRig(t)

	s := rx.streams[vitalsRigSession]
	dst := make([]int16, FrameSamples)
	jitterPrime(s.jit, 0, jitterStartFrames)
	for range jitterStartFrames {
		s.jit.Pop(dst)
	}
	before := rx.vitals()

	rx.close()
	after := rx.vitals()
	if after.Played != before.Played {
		t.Errorf("played = %d after close, want %d kept", after.Played, before.Played)
	}
}

// Depth is the deepest sender, not the last one iterated: a map has no order,
// and one peer on a bursting link is the reason the panel is being read.
func TestDepthReportsTheWorstSenderNotAnArbitraryOne(t *testing.T) {
	t.Parallel()
	rx, _ := newVitalsRig(t)

	shallow := rx.streams[vitalsRigSession]
	deep := newVitalsStream(t, 99, "deep")
	for range 7 {
		deep.jit.grow()
	}
	rx.streams[deep.session] = deep

	rx.streams[100] = newVitalsStream(t, 100, "shallow too")

	want := deep.jit.Depth()
	if want <= shallow.jit.Depth() {
		t.Fatalf("the rig did not make one sender deeper: %d vs %d", want, shallow.jit.Depth())
	}
	// Repeatedly, because Go randomises map iteration order: a single reading
	// would agree with "whichever sender came last" a third of the time, and a
	// test that passes by luck is not a test.
	for i := range 30 {
		if got := rx.vitals().Depth; got != want {
			t.Fatalf("reading %d: depth = %d, want %d (the deepest sender)", i, got, want)
		}
	}
}

// captureHandler collects the records written to it, so a test can ask what
// the log would actually contain.
type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) lines(message string) int {
	n := 0
	for _, r := range h.records {
		if r.Message == message {
			n++
		}
	}
	return n
}

// capturing replaces the rig's logger so the panel can be read back.
func capturing(rx *rxPipeline) *captureHandler {
	h := &captureHandler{}
	rx.log = slog.New(h)
	return h
}

// The panel has to reach the log on its own, without anything asking for it.
// This is the whole reason the work was done: the counters existed before and
// no production line read them, so every incident arrived without numbers.
func TestThePanelReachesTheLogWhileSomebodyIsSpeaking(t *testing.T) {
	t.Parallel()
	rx, _ := newVitalsRig(t)
	log := capturing(rx)

	var last VoiceVitals
	rx.logVitals(voiceVitalsTicks, &last)

	if got := log.lines("voice vitals"); got != 1 {
		t.Fatalf("wrote %d panels with a sender live, want 1", got)
	}
	if last.Streams != 1 {
		t.Errorf("the remembered reading says %d streams, want 1", last.Streams)
	}
}

// Every tick would be a line every ten milliseconds, which is not a log.
func TestThePanelKeepsItsOwnCadence(t *testing.T) {
	t.Parallel()
	rx, _ := newVitalsRig(t)
	log := capturing(rx)

	var last VoiceVitals
	for tick := 1; tick < voiceVitalsTicks; tick++ {
		rx.logVitals(tick, &last)
	}
	if got := log.lines("voice vitals"); got != 0 {
		t.Fatalf("wrote %d panels before the first cadence tick, want 0", got)
	}
}

// A room with nobody in it must not write anything, or the archive fills with
// zeros and a gap in the log stops meaning anything.
func TestAnEmptyRoomWritesNothing(t *testing.T) {
	t.Parallel()
	rx, _ := newVitalsRig(t)
	delete(rx.streams, vitalsRigSession)
	log := capturing(rx)

	var last VoiceVitals
	for tick := voiceVitalsTicks; tick <= 10*voiceVitalsTicks; tick += voiceVitalsTicks {
		rx.logVitals(tick, &last)
	}
	if got := log.lines("voice vitals"); got != 0 {
		t.Fatalf("wrote %d panels for an empty room, want 0", got)
	}
}

// A word spoken and finished between two cadence ticks still has to be
// reported: nobody is live when the reading is taken, but audio moved, and
// that is exactly the short glitch a user would be describing.
func TestAWordThatEndedBetweenTicksIsStillReported(t *testing.T) {
	t.Parallel()
	rx, sink := newVitalsRig(t)
	log := capturing(rx)

	s := rx.streams[vitalsRigSession]
	dst := make([]int16, FrameSamples)
	jitterPrime(s.jit, 0, jitterStartFrames)
	for range jitterStartFrames {
		s.jit.Pop(dst)
	}
	// The sender goes away before the panel is next written.
	s.jit.Reset()
	s.lastPacket = time.Now().Add(-2 * streamIdleTimeout)
	rx.tick(sink, false, &userAudioState{}, 0)
	if len(rx.streams) != 0 {
		t.Fatal("the stream was not released; this test proves nothing")
	}

	var last VoiceVitals
	rx.logVitals(voiceVitalsTicks, &last)
	if got := log.lines("voice vitals"); got != 1 {
		t.Fatalf("wrote %d panels for a word that ended between ticks, want 1", got)
	}

	// And having said it once, it must not keep saying it.
	rx.logVitals(2*voiceVitalsTicks, &last)
	if got := log.lines("voice vitals"); got != 1 {
		t.Fatalf("wrote %d panels, want the reading reported once and then quiet", got)
	}
}
