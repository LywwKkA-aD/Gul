package notify

import (
	"sync"
	"testing"
	"time"
)

var epoch = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func hidden() Window  { return Window{Visible: false, Focused: false} }
func behind() Window  { return Window{Visible: true, Focused: false} }
func watched() Window { return Window{Visible: true, Focused: true} }

// The rule this whole feature exists to obey: nothing is ever sent while the
// user is looking at the window.
func TestNothingIsSentWhileTheWindowIsFocused(t *testing.T) {
	t.Parallel()
	d := New(DefaultBurst, DefaultRefill)
	d.SetWindow(watched())

	for i := range 10 {
		at := epoch.Add(time.Duration(i) * time.Minute)
		if d.Should(KindMessage, at) {
			t.Fatalf("notified on a focused window at %v", at)
		}
		if d.Should(KindJoin, at) {
			t.Fatalf("notified a join on a focused window at %v", at)
		}
	}
}

// A window the user is not looking at, whether it is hidden behind another
// application or hidden outright.
func TestSentWhenTheWindowIsNotAttended(t *testing.T) {
	t.Parallel()
	for name, w := range map[string]Window{"hidden": hidden(), "behind another window": behind()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := New(DefaultBurst, DefaultRefill)
			d.SetWindow(w)
			if !d.Should(KindMessage, epoch) {
				t.Fatal("a message went unnotified")
			}
			if !d.Should(KindJoin, epoch) {
				t.Fatal("a join went unnotified")
			}
		})
	}
}

// A default decider is silent: a platform that never tells us about focus must
// not notify over a window the user may well be reading.
func TestTheDefaultIsSilence(t *testing.T) {
	t.Parallel()
	d := New(0, 0)
	if !d.Window().Attended() {
		t.Fatal("a fresh decider does not assume the user is present")
	}
	if d.Should(KindMessage, epoch) {
		t.Fatal("notified before anything told us where the window is")
	}
}

// Only some events are worth interrupting somebody for. Leaving plays its cue
// and stops there.
func TestOnlyNotifiableKinds(t *testing.T) {
	t.Parallel()
	tests := map[Kind]bool{
		KindMessage:  true,
		KindJoin:     true,
		KindLeave:    false,
		Kind(""):     false,
		Kind("mute"): false,
	}
	for kind, want := range tests {
		d := New(DefaultBurst, DefaultRefill)
		d.SetWindow(hidden())
		if got := d.Should(kind, epoch); got != want {
			t.Errorf("Should(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestBurstThenOnePerRefill(t *testing.T) {
	t.Parallel()
	const refill = 20 * time.Second
	d := New(3, refill)
	d.SetWindow(hidden())

	// The burst goes through back to back.
	for i := range 3 {
		if !d.Should(KindMessage, epoch) {
			t.Fatalf("message %d of the burst was dropped", i)
		}
	}
	// The fourth is not a notification, whatever the channel is doing.
	for i := range 20 {
		at := epoch.Add(time.Duration(i) * time.Second)
		if d.Should(KindMessage, at) {
			t.Fatalf("the bucket handed out a token at %v", at)
		}
	}

	// One token comes back per refill period.
	if !d.Should(KindMessage, epoch.Add(refill)) {
		t.Fatal("no token after a full refill period")
	}
	if d.Should(KindMessage, epoch.Add(refill+time.Second)) {
		t.Fatal("two tokens for one refill period")
	}
	if !d.Should(KindMessage, epoch.Add(2*refill)) {
		t.Fatal("no token after the second refill period")
	}
}

// A long quiet stretch refills the bucket, but never past its capacity: coming
// back after an hour must not release an hour's worth of notifications.
func TestTheBucketDoesNotOverfill(t *testing.T) {
	t.Parallel()
	d := New(3, 20*time.Second)
	d.SetWindow(hidden())
	for range 3 {
		if !d.Should(KindMessage, epoch) {
			t.Fatal("the initial burst was short")
		}
	}

	late := epoch.Add(time.Hour)
	sent := 0
	for range 10 {
		if d.Should(KindMessage, late) {
			sent++
		}
	}
	if sent != 3 {
		t.Fatalf("%d notifications after an hour of quiet, want the burst of 3", sent)
	}
}

// Events that happen while the user is watching must not silently spend the
// burst they will need the moment they walk away.
func TestAttendedEventsCostNoTokens(t *testing.T) {
	t.Parallel()
	d := New(3, time.Hour)
	d.SetWindow(watched())
	for range 50 {
		d.Should(KindMessage, epoch)
	}

	d.SetWindow(hidden())
	for i := range 3 {
		if !d.Should(KindMessage, epoch) {
			t.Fatalf("token %d was already spent while the window was watched", i)
		}
	}
}

// A clock that steps backwards (a manual change, an NTP correction) must not
// release the bucket or wedge it shut.
func TestABackwardsClockChangesNothing(t *testing.T) {
	t.Parallel()
	d := New(1, time.Minute)
	d.SetWindow(hidden())
	if !d.Should(KindMessage, epoch) {
		t.Fatal("the first message was dropped")
	}
	if d.Should(KindMessage, epoch.Add(-time.Hour)) {
		t.Fatal("a backwards clock handed out a token")
	}
	if !d.Should(KindMessage, epoch.Add(-time.Hour).Add(time.Minute)) {
		t.Fatal("the bucket never refilled after the clock stepped back")
	}
}

// The window state is written from platform events while chat and channel
// changes arrive on their own goroutines.
func TestDeciderIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	d := New(DefaultBurst, DefaultRefill)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := range 200 {
				d.SetWindow(Window{Visible: true, Focused: j%2 == 0})
				d.Should(KindMessage, epoch.Add(time.Duration(i*j)*time.Second))
			}
		}(i)
	}
	wg.Wait()
}
