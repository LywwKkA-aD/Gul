package relay

import "time"

// The sequence every door has to run before a byte of anyone's traffic is
// carried: is this source banned, does the credential hold, did a concurrent
// request ban the source while this one was checking.
//
// It lived twice, once in ServeHTTP and once in serveConn, in the same order
// with the same recheck and the same comment about the race. Two copies of a
// defence sequence are two chances to add a third that quietly omits a step,
// and the step most easily omitted is the recheck - it looks redundant and is
// not.
//
// admit decides and returns; it writes nothing. That is the whole difference
// between the doors: the HTTP one owes an answer and turns a verdict into a
// page from the cover site, the QUIC one owes nothing and turns it into
// silence. Logging stays with the callers too, because the journal names the
// road and an operator greps it that way.

// verdict is what admit decided.
type verdict uint8

const (
	// admitted means the caller may go on to acquire capacity.
	admitted verdict = iota
	// admitBanned means this source is inside a temporary ban. retryAfter
	// says for how long.
	admitBanned
	// admitRejected means the credential did not hold. class describes its
	// shape - never its value - and the ban fields say whether this failure
	// was the one that started a ban.
	admitRejected
)

// admission carries the verdict and everything a caller needs to answer and to
// log, without any of it having been written yet.
type admission struct {
	verdict verdict
	// retryAfter is how long the ban has left, on admitBanned and on a
	// rejection that tripped one.
	retryAfter time.Duration
	// class is the shape of the credential presented: absent, malformed, or a
	// well-formed one. The credential itself never appears here.
	class string
	// limited says the rejection also leaves the source rate limited, and
	// activated says this failure is the one that started the ban. They are
	// separate because only the first is worth a warning: the rest of a
	// flood is chosen by whoever is knocking.
	limited, activated bool
}

// admit runs the sequence in the order it has to run, and returns what it
// decided.
func (h *Handler) admit(sourceBlock, authorization string) admission {
	if retryAfter, banned := h.authFailures.banRemaining(sourceBlock); banned {
		return admission{verdict: admitBanned, retryAfter: retryAfter}
	}

	result := h.authorize(authorization)
	if !result.authorized {
		ban := h.authFailures.recordFailure(sourceBlock)
		return admission{
			verdict:    admitRejected,
			class:      result.class,
			retryAfter: ban.retryAfter,
			limited:    ban.limited,
			activated:  ban.activated,
		}
	}

	// A concurrent failed request may have activated the ban while this one
	// was validating its credential. Recheck under the limiter lock before
	// accepting it. This is the step that reads as redundant and is not.
	if retryAfter, banned := h.authFailures.clearIfAllowed(sourceBlock); banned {
		return admission{verdict: admitBanned, retryAfter: retryAfter}
	}
	return admission{verdict: admitted}
}
