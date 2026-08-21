package audio

import (
	"math"
	"testing"
	"time"
)

// driftBase is a fixed wall clock origin: the estimator only ever looks at
// differences, so any deterministic origin works.
var driftBase = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// driftFeed pushes ticks samples of a device running at rateHz, starting
// one step after start and counting up from frames. It returns the state
// the caller needs to keep feeding: the last counter value and its time.
func driftFeed(d *Drift, frames uint64, start time.Time, rateHz float64, step time.Duration, ticks int) (uint64, time.Time) {
	last, at := frames, start
	for i := 1; i <= ticks; i++ {
		at = start.Add(time.Duration(i) * step)
		last = frames + uint64(math.Round(rateHz*step.Seconds()*float64(i)))
		d.Sample(last, at)
	}
	return last, at
}

func TestDriftPPM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		rateHz float64
		want   float64
	}{
		{"nominal", 48000, 0},
		{"fast device", 48048, +1000},
		{"slow device", 47952, -1000},
		{"slightly fast", 48000 * (1 + 50e-6), +50},
		{"slightly slow", 48000 * (1 - 50e-6), -50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := NewDrift()
			d.Sample(0, driftBase)
			driftFeed(d, 0, driftBase, tc.rateHz, time.Second, 15)

			got, ok := d.PPM()
			if !ok {
				t.Fatalf("PPM not ready after a full window")
			}
			if math.Abs(got-tc.want) > 5 {
				t.Fatalf("PPM = %v, want %v (+-5)", got, tc.want)
			}
		})
	}
}

func TestDriftNeedsEnoughData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(d *Drift)
	}{
		{"no samples", func(*Drift) {}},
		{"one sample", func(d *Drift) {
			d.Sample(0, driftBase)
		}},
		{"span below the minimum", func(d *Drift) {
			d.Sample(0, driftBase)
			driftFeed(d, 0, driftBase, SampleRate, time.Second, 2)
		}},
		{"reset after a full window", func(d *Drift) {
			d.Sample(0, driftBase)
			driftFeed(d, 0, driftBase, SampleRate, time.Second, 15)
			d.Reset()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := NewDrift()
			tc.setup(d)

			got, ok := d.PPM()
			if ok {
				t.Fatalf("PPM = %v, ok = true, want not ready", got)
			}
			if got != 0 {
				t.Fatalf("PPM = %v, want 0 when not ready", got)
			}
		})
	}
}

func TestDriftStalledCounter(t *testing.T) {
	t.Parallel()

	d := NewDrift()
	d.Sample(0, driftBase)
	frames, at := driftFeed(d, 0, driftBase, SampleRate, time.Second, 15)
	if _, ok := d.PPM(); !ok {
		t.Fatalf("PPM not ready before the stall")
	}

	// The device stopped but the engine keeps polling the counter. The
	// reading must never become NaN or Inf, and once the window holds
	// nothing but the frozen counter there is no rate to report.
	for i := 1; i <= 20; i++ {
		d.Sample(frames, at.Add(time.Duration(i)*time.Second))
		got, _ := d.PPM()
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("second %d of the stall: PPM = %v, want a finite value", i, got)
		}
	}

	got, ok := d.PPM()
	if ok {
		t.Fatalf("PPM = %v, ok = true, want not ready on a frozen counter", got)
	}
	if got != 0 {
		t.Fatalf("PPM = %v, want 0 on a frozen counter", got)
	}
}

func TestDriftCounterRestartResetsWindow(t *testing.T) {
	t.Parallel()

	d := NewDrift()
	d.Sample(0, driftBase)
	_, at := driftFeed(d, 0, driftBase, SampleRate, time.Second, 15)

	// The device restarted: the counter rewound. The old window belongs
	// to a different stream, so the estimator must start over instead of
	// reporting an enormous negative drift.
	restart := at.Add(time.Second)
	d.Sample(0, restart)
	if got, ok := d.PPM(); ok {
		t.Fatalf("PPM = %v, ok = true right after a counter restart, want a cleared window", got)
	}

	driftFeed(d, 0, restart, 48048, time.Second, 15)
	got, ok := d.PPM()
	if !ok {
		t.Fatalf("PPM not ready after the restarted stream filled the window")
	}
	if math.Abs(got-1000) > 5 {
		t.Fatalf("PPM = %v, want +1000 (+-5) after the restart", got)
	}
}

func TestDriftClockGoingBackwardsResetsWindow(t *testing.T) {
	t.Parallel()

	d := NewDrift()
	d.Sample(0, driftBase)
	frames, at := driftFeed(d, 0, driftBase, SampleRate, time.Second, 15)

	d.Sample(frames+SampleRate, at.Add(-5*time.Second))
	if got, ok := d.PPM(); ok {
		t.Fatalf("PPM = %v, ok = true after the clock went backwards, want a cleared window", got)
	}
}

func TestDriftWindowSlidesToTheCurrentRate(t *testing.T) {
	t.Parallel()

	// A device that changes rate must not be judged by its history for
	// longer than the window.
	d := NewDrift()
	d.Sample(0, driftBase)
	frames, at := driftFeed(d, 0, driftBase, 47952, time.Second, 15)
	if got, _ := d.PPM(); math.Abs(got+1000) > 5 {
		t.Fatalf("PPM = %v, want -1000 (+-5) for the first rate", got)
	}

	driftFeed(d, frames, at, 48048, time.Second, 30)
	got, ok := d.PPM()
	if !ok {
		t.Fatalf("PPM not ready after the rate change")
	}
	if math.Abs(got-1000) > 5 {
		t.Fatalf("PPM = %v, want +1000 (+-5) after the window slid past the rate change", got)
	}
}

func TestDriftToleratesFrequentSampling(t *testing.T) {
	t.Parallel()

	// Sampling far more often than once a second must not shrink the
	// window: the estimator thins the samples out itself.
	d := NewDrift()
	d.Sample(0, driftBase)
	driftFeed(d, 0, driftBase, 48048, 10*time.Millisecond, 1500)

	got, ok := d.PPM()
	if !ok {
		t.Fatalf("PPM not ready after 15 s of 10 ms sampling")
	}
	if math.Abs(got-1000) > 5 {
		t.Fatalf("PPM = %v, want +1000 (+-5)", got)
	}

	first, last := d.at(0), d.at(d.count-1)
	if span := last.at.Sub(first.at); span < driftWindow-time.Second || span > driftWindow {
		t.Fatalf("retained window spans %v, want about %v", span, driftWindow)
	}
	if d.count > driftCapacity {
		t.Fatalf("retained %d samples, capacity is %d", d.count, driftCapacity)
	}
}

func TestDriftWindowIsBounded(t *testing.T) {
	t.Parallel()

	d := NewDrift()
	d.Sample(0, driftBase)
	driftFeed(d, 0, driftBase, SampleRate, time.Second, 120)

	if span := d.at(d.count - 1).at.Sub(d.at(0).at); span > driftWindow+time.Second {
		t.Fatalf("retained window spans %v, want at most about %v", span, driftWindow)
	}
}

func BenchmarkDriftPPM(b *testing.B) {
	d := NewDrift()
	d.Sample(0, driftBase)
	driftFeed(d, 0, driftBase, 48048, time.Second, 15)

	b.ReportAllocs()
	for b.Loop() {
		d.PPM()
	}
}
