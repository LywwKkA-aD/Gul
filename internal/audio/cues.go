package audio

import "math"

// Cue identifies a built-in sound.
//
// Cues are UI feedback played through the voice engine rather than by the
// WebView, and the reason is acoustic, not stylistic: a sound the WebView
// plays goes to the system default output instead of the playback device
// the user picked, and it never reaches the AEC3 reference, so the
// microphone hears it as echo nobody cancels. Mixing it into the receive
// path fixes both at once (PLAN.md 4.4).
type Cue int

const (
	CueJoin Cue = iota
	CueLeave
	CueMuted
	CueUnmuted

	// cueCount bounds the clip table; not a cue.
	cueCount
)

const (
	// cueVolumeDefault is the shipped cue gain. Halving the clip peak puts
	// the loudest cue near -20 dBFS: audible over a conversation without
	// competing with it.
	cueVolumeDefault = 0.5

	// cuePeakFS is the peak amplitude of every clip in full scale units at
	// gain 1, which puts them at about -14 dBFS - inside the -12 dBFS
	// ceiling a UI sound must never cross. The mixer sums cues with voice,
	// so a loud clip would eat the headroom the soft-clip needs.
	cuePeakFS = 0.2

	// cueFadeSamples is the raised-cosine fade at both ends of every note.
	// 5 ms kills the click of a sine that starts and stops mid-cycle
	// without audibly shortening a 70 ms note.
	cueFadeSamples = 5 * SampleRate / 1000

	// Note lengths in 10 ms frames, so every clip is a whole number of
	// ticks and the DSP loop never carries a partial frame.
	cuePairNoteFrames   = 7
	cueSingleNoteFrames = 9
)

// Note frequencies. The pair cues move by a perfect fourth, which reads as
// a deliberate two-note figure rather than a glitch; the two single cues sit
// a perfect fifth apart (A4 to E5) so mute and unmute are told apart by ear.
const (
	cueToneLow  = 440.00 // A4
	cueToneMid  = 659.25 // E5
	cueToneHigh = 880.00 // A5
)

// cueClips holds every cue pre-rendered to the project grid (48 kHz mono,
// int16) once, at package init. The DSP tick then only copies: no
// synthesis, no asset files, no decoding on the audio path.
var cueClips = renderCues()

func renderCues() [cueCount][]int16 {
	var clips [cueCount][]int16
	clips[CueJoin] = renderClip(cuePairNoteFrames, cueToneMid, cueToneHigh)
	clips[CueLeave] = renderClip(cuePairNoteFrames, cueToneHigh, cueToneMid)
	clips[CueMuted] = renderClip(cueSingleNoteFrames, cueToneLow)
	clips[CueUnmuted] = renderClip(cueSingleNoteFrames, cueToneMid)
	return clips
}

// renderClip lays the notes end to end, each one frames long.
func renderClip(frames int, notes ...float64) []int16 {
	n := frames * FrameSamples
	clip := make([]int16, len(notes)*n)
	for i, freq := range notes {
		renderNote(clip[i*n:(i+1)*n], freq)
	}
	return clip
}

// renderNote writes one sine note with raised-cosine fades at both ends.
// Each note restarts the phase at zero and the fades bring the envelope
// back to zero, so the junction between two notes is continuous and the
// clip begins and ends in silence.
func renderNote(dst []int16, freq float64) {
	last := len(dst) - 1
	for i := range dst {
		env := 1.0
		if i < cueFadeSamples {
			env = cueFade(i)
		} else if tail := last - i; tail < cueFadeSamples {
			env = cueFade(tail)
		}
		v := cuePeakFS * fullScale * env * math.Sin(2*math.Pi*freq*float64(i)/SampleRate)
		dst[i] = toInt16(float32(v))
	}
}

// cueFade is the raised-cosine ramp, 0 at i = 0 and 1 at the fade length.
func cueFade(i int) float64 {
	return 0.5 - 0.5*math.Cos(math.Pi*float64(i)/cueFadeSamples)
}

// cuePlayer is the cue playback state: which clip and how far into it. It
// belongs to the DSP goroutine exclusively (PLAN.md 4.6) - the pending slot
// in engineState is the only cross-goroutine hop.
type cuePlayer struct {
	clip []int16
	pos  int
	gain float32
}

// start begins playing c. The caller only starts a cue on an idle player:
// restarting a clip mid-way would put a step of up to the clip's peak into
// the output - an audible click - so a cue that arrives during playback
// waits in the pending slot until the current clip (at most ~140 ms) ends.
func (p *cuePlayer) start(c Cue) {
	if c < 0 || c >= cueCount {
		return
	}
	p.clip, p.pos = cueClips[c], 0
}

// mix adds the next cue frame to the mixer, exactly like one more voice
// stream. Zero allocations on the tick: the frame is a subslice of the
// pre-rendered clip.
func (p *cuePlayer) mix(m *Mixer) {
	if p.clip == nil {
		return
	}
	n := min(FrameSamples, len(p.clip)-p.pos)
	m.Add(p.clip[p.pos:p.pos+n], p.gain)
	p.pos += n
	if p.pos >= len(p.clip) {
		p.clip, p.pos = nil, 0
	}
}

// PlayCue schedules a cue on the DSP goroutine. It never blocks: one atomic
// compare-and-swap, callable from any goroutine including a Wails binding.
// At most one cue is pending, so a burst of channel events cannot queue a
// chain of beeps - a second call before the DSP goroutine takes the first
// one is dropped.
func (e *Engine) PlayCue(c Cue) {
	if c < 0 || c >= cueCount {
		return
	}
	e.state.cue.CompareAndSwap(0, int32(c)+1)
}

// SetCueVolume sets the cue gain, clamped to [0, 1]; 0 turns cues off.
func (e *Engine) SetCueVolume(v float32) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	e.state.cueVol.Store(math.Float32bits(v))
}

// dropPendingCue forgets a cue queued during teardown: it belongs to the
// session that ended, and the next Start must not open with a stale beep.
func (e *Engine) dropPendingCue() { e.state.cue.Store(0) }

func (e *Engine) cueGain() float32 {
	return math.Float32frombits(e.state.cueVol.Load())
}

// takeCue consumes the pending slot, emptying it. DSP goroutine only.
func (e *Engine) takeCue() (Cue, bool) {
	v := e.state.cue.Swap(0)
	if v == 0 {
		return 0, false
	}
	return Cue(v - 1), true
}

// applyCue moves the pending cue and the current gain onto the DSP
// goroutine's player; runs on the DSP goroutine every tick, next to
// applyGateSettings.
func (e *Engine) applyCue(p *cuePlayer) {
	p.gain = e.cueGain()
	if p.playing() {
		return
	}
	if c, ok := e.takeCue(); ok {
		p.start(c)
	}
}

func (p *cuePlayer) playing() bool { return p.clip != nil }
