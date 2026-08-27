package relay

import (
	"sync"
	"testing"
	"time"
)

// One sequence, two doors. The point of admit is that a door cannot answer
// without having run it, so the test asks the sequence itself rather than
// either door: the doors are covered where they answer, here we pin what they
// are answering about.
func TestAdmissionRunsTheWholeSequenceInOrder(t *testing.T) {
	t.Parallel()
	const source = "192.0.2.0"

	t.Run("a credential that holds is admitted", func(t *testing.T) {
		t.Parallel()
		h := mustHandler(t, baseConfig(defaultTestSecret))
		if got := h.admit(source, bearerHeader(defaultTestSecret).Get("Authorization")); got.verdict != admitted {
			t.Fatalf("verdict = %v, want admitted", got.verdict)
		}
	})

	t.Run("a wrong credential is rejected and named by shape", func(t *testing.T) {
		t.Parallel()
		h := mustHandler(t, baseConfig(defaultTestSecret))
		got := h.admit(source, bearerHeader("the wrong secret").Get("Authorization"))
		if got.verdict != admitRejected {
			t.Fatalf("verdict = %v, want admitRejected", got.verdict)
		}
		if got.class == "" {
			t.Error("no credential class; the journal would say nothing about the shape")
		}
	})

	t.Run("enough failures leave the source banned", func(t *testing.T) {
		t.Parallel()
		h := mustHandler(t, baseConfig(defaultTestSecret))
		wrong := bearerHeader("the wrong secret").Get("Authorization")

		var activated int
		for range 16 {
			if decision := h.admit(source, wrong); decision.activated {
				activated++
			}
		}
		// Only the failure that starts a ban is worth a warning; the rest of a
		// flood is chosen by whoever is knocking.
		if activated != 1 {
			t.Fatalf("ban activated %d times, want exactly 1", activated)
		}

		// And the ban holds against a credential that would otherwise pass.
		// This is the step that reads as redundant and is not: a door that
		// skipped it would accept a source another door had just banned.
		good := h.admit(source, bearerHeader(defaultTestSecret).Get("Authorization"))
		if good.verdict != admitBanned {
			t.Fatalf("verdict = %v during a ban, want admitBanned", good.verdict)
		}
		if good.retryAfter <= 0 || good.retryAfter > time.Hour {
			t.Fatalf("retry_after = %v, want a sane wait", good.retryAfter)
		}
	})
}

// The recheck under the limiter lock guards a window, not a state: a ban that
// activates between the first check and the credential validating. The
// sequential test above cannot reach it, because by then the opening check
// already answers. This one runs the window - failures and good credentials
// against one source at once - and asserts the invariant that window exists
// for: once the source is banned, nothing is admitted afterwards.
//
// It cannot promise to hit the window on any given run. It can promise that
// hitting it is not a data race, and that the outcome stays legal.
func TestNoAdmissionSurvivesTheBanItRacedWith(t *testing.T) {
	t.Parallel()
	h := mustHandler(t, baseConfig(defaultTestSecret))
	good := bearerHeader(defaultTestSecret).Get("Authorization")
	wrong := bearerHeader("the wrong secret").Get("Authorization")
	const source = "192.0.2.1"

	var mu sync.Mutex
	banned := false

	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential := good
			if worker%2 == 0 {
				credential = wrong
			}
			for range 32 {
				if decision := h.admit(source, credential); decision.verdict == admitBanned {
					mu.Lock()
					banned = true
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if !banned {
		t.Fatal("no ban was reached; the window was never opened")
	}

	// Counting admissions that landed after the flag flipped would be the
	// wrong assertion, and it was: an admit that began before the ban may
	// finish after it, so the count is legal and the test was flaky. What is
	// actually promised is a state, not an ordering - once the workers have
	// stopped, a credential that would otherwise pass is still refused.
	if got := h.admit(source, good); got.verdict != admitBanned {
		t.Fatalf("verdict after the storm = %v, want the ban to still hold", got.verdict)
	}
}
