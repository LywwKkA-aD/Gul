package audio

import "time"

const (
	// driftWindow is the sliding window the rate is estimated over
	// (PLAN.md 4.2). Long enough that the per sample timestamp jitter
	// averages out, short enough to follow a device that changes rate.
	driftWindow = 10 * time.Second

	// driftMinSpan is the shortest window that yields a meaningful ppm.
	// Below it the timestamp jitter of the sampling goroutine dominates
	// the measured rate.
	driftMinSpan = 4 * time.Second

	// driftMinInterval is the smallest gap between retained samples. A
	// caller that samples far more often than once a second adds no
	// information, and dropping the extra samples keeps the retained
	// window driftWindow long instead of letting it shrink to the ring
	// capacity.
	driftMinInterval = 200 * time.Millisecond

	// driftCapacity covers driftWindow at driftMinInterval with slack,
	// so eviction is by age and never by capacity in practice.
	driftCapacity = 64

	// ppmScale converts a relative rate deviation into parts per million.
	ppmScale = 1e6
)

type driftSample struct {
	frames uint64
	at     time.Time
}

// Drift estimates a device clock rate against the wall clock from
// (total frames, time) samples over a sliding window (~10 s).
//
// M2 uses it as a logged metric only; the reference track correction
// (drop/dup of one frame) lands in M3 (PLAN.md 4.2, 7).
//
// Single-goroutine use: it belongs to whoever polls the device counter.
type Drift struct {
	buf   [driftCapacity]driftSample
	head  int // index of the oldest retained sample
	count int
}

// NewDrift returns an empty estimator.
func NewDrift() *Drift {
	return &Drift{}
}

// Sample records the device's cumulative frame counter at time now.
// Call it periodically (e.g. once a second) from the engine.
func (d *Drift) Sample(totalFrames uint64, now time.Time) {
	if d.count > 0 {
		last := d.at(d.count - 1)
		switch {
		case totalFrames < last.frames || now.Before(last.at):
			// The device restarted (counter rewound) or the clock went
			// backwards: everything in the window describes a different
			// stream, so start over from this sample.
			d.Reset()
		case now.Sub(last.at) < driftMinInterval:
			return
		}
	}
	d.push(driftSample{frames: totalFrames, at: now})
	d.evict(now)
}

// PPM reports the deviation from the nominal SampleRate in parts per
// million (+ = device clock fast) and whether the window has enough
// data to be meaningful.
func (d *Drift) PPM() (float64, bool) {
	if d.count < 2 {
		return 0, false
	}
	first, last := d.at(0), d.at(d.count-1)
	span := last.at.Sub(first.at)
	if span < driftMinSpan {
		return 0, false
	}
	if last.frames <= first.frames {
		// The counter is stalled (device stopped but still polled):
		// there is no rate to report, and reporting one would look like
		// an enormous negative drift.
		return 0, false
	}

	// Least squares slope of frames over seconds. Both axes are taken
	// relative to the first sample so the values stay small and exact;
	// the regression rejects the scheduling jitter of the sampling
	// goroutine far better than a two point estimate.
	n := float64(d.count)
	var sumT, sumF float64
	for i := range d.count {
		s := d.at(i)
		sumT += s.at.Sub(first.at).Seconds()
		sumF += float64(s.frames - first.frames)
	}
	meanT, meanF := sumT/n, sumF/n

	var num, den float64
	for i := range d.count {
		s := d.at(i)
		dt := s.at.Sub(first.at).Seconds() - meanT
		num += dt * (float64(s.frames-first.frames) - meanF)
		den += dt * dt
	}
	if den <= 0 {
		return 0, false
	}
	rate := num / den // device frames per second of wall clock
	if rate <= 0 {
		return 0, false
	}
	return (rate/SampleRate - 1) * ppmScale, true
}

// Reset clears the window (device change, stream restart).
func (d *Drift) Reset() {
	d.head = 0
	d.count = 0
}

func (d *Drift) at(i int) driftSample {
	return d.buf[(d.head+i)%driftCapacity]
}

func (d *Drift) push(s driftSample) {
	if d.count == driftCapacity {
		d.dropOldest()
	}
	d.buf[(d.head+d.count)%driftCapacity] = s
	d.count++
}

// evict drops samples that fell out of the window, always keeping the
// newest one so a long pause in sampling cannot empty the estimator.
func (d *Drift) evict(now time.Time) {
	for d.count > 1 && now.Sub(d.at(0).at) > driftWindow {
		d.dropOldest()
	}
}

func (d *Drift) dropOldest() {
	d.head = (d.head + 1) % driftCapacity
	d.count--
}
