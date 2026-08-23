package core

import (
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

func pttEvents(em *fakeEmitter) []bool {
	var out []bool
	for _, ev := range em.all() {
		if ev.name != domain.EventAudioPTT {
			continue
		}
		out = append(out, ev.payload.(domain.PTTState).Held)
	}
	return out
}

// SetPTT pushes EventAudioPTT so the mic indicator can follow a global key,
// but only when the held state actually changes: a repeated value is silent.
func TestSetPTTEmitsOnlyOnChange(t *testing.T) {
	t.Parallel()
	app, _, em := newTestApp(t)
	voice := &fakeVoice{}
	app.SetVoice(voice)

	app.SetPTT(true)  // change -> emit true
	app.SetPTT(true)  // no change -> silent
	app.SetPTT(false) // change -> emit false
	app.SetPTT(false) // no change -> silent

	got := pttEvents(em)
	want := []bool{true, false}
	if len(got) != len(want) {
		t.Fatalf("ptt events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ptt events = %v, want %v", got, want)
		}
	}
	if n := len(voice.ptt); n == 0 || voice.ptt[n-1] {
		t.Fatalf("engine ptt trail = %v, want it to end released", voice.ptt)
	}
}
