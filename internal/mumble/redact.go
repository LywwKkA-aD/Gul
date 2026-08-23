package mumble

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// redactedServer replaces every spelling of the server the user connected to.
const redactedServer = "<server>"

// The two shapes an address literal takes in a network error, once name
// resolution has replaced the host the user typed: a bracketed IPv6 literal
// and four dotted decimal groups, each with an optional port. Both
// over-match on purpose - net.ParseIP decides what is an address.
//
// They are applied in turn rather than as one alternation so that a bracketed
// value which is not an address ("map[host:203.0.113.7]") still has its
// contents examined instead of being swallowed whole and kept.
var (
	ipv6Candidate = regexp.MustCompile(`\[[0-9A-Za-z.:%]+\](:\d{1,5})?`)
	ipv4Candidate = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b(:\d{1,5})?`)
)

// RedactServer removes the connection arguments from text.
//
// gul.log ends up inside a diagnostics archive the user is expected to hand to
// somebody else (PLAN.md §10.7), so the server the user typed must not survive
// into a log record - not as the configured address, not as the host:port pair
// that network errors embed on their own, and not as the resolved IP those
// errors report instead of the name: "dial tcp 203.0.113.7:443" identifies the
// server just as precisely as its hostname does.
func RedactServer(text, address string) string {
	if text == "" {
		return text
	}
	for _, spelling := range serverSpellings(address) {
		text = strings.ReplaceAll(text, spelling, redactedServer)
	}
	return redactIPLiterals(text)
}

// redactIPLiterals removes the address literals a network error reports on its
// own. Every candidate is confirmed with net.ParseIP before it disappears: a
// record that loses "map[state:connecting]" or a "999.999.999.999 ms" to an
// eager pattern loses the diagnosis the archive was collected for.
func redactIPLiterals(text string) string {
	for _, candidates := range []*regexp.Regexp{ipv6Candidate, ipv4Candidate} {
		text = candidates.ReplaceAllStringFunc(text, func(candidate string) string {
			if isIPLiteral(candidate) {
				return redactedServer
			}
			return candidate
		})
	}
	return text
}

func isIPLiteral(candidate string) bool {
	if host, _, err := net.SplitHostPort(candidate); err == nil {
		candidate = host
	}
	candidate = strings.TrimSuffix(strings.TrimPrefix(candidate, "["), "]")
	// A link-local address carries the interface it is scoped to; the address
	// in front of the zone is still the address.
	if zone := strings.IndexByte(candidate, '%'); zone >= 0 {
		candidate = candidate[:zone]
	}
	return net.ParseIP(candidate) != nil
}

// serverSpellings lists what has to disappear, longest first so a full URL is
// replaced as a whole rather than leaving its scheme and path behind.
func serverSpellings(address string) []string {
	if address == "" {
		return nil
	}
	spellings := []string{address}
	if ep, err := parseEndpoint(address); err == nil {
		spellings = append(spellings, ep.address, ep.host)
	} else if host := rawHost(address); host != "" {
		spellings = append(spellings, host)
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(spellings))
	for _, spelling := range spellings {
		if spelling == "" || seen[spelling] {
			continue
		}
		seen[spelling] = true
		out = append(out, spelling)
	}
	// Insertion order is already address, canonical address, host; only the
	// first two can differ in length, so a stable sort by length is enough.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j]) > len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// rawHost recovers a host from an address that failed validation, so even a
// rejected address is redacted out of the record that reports the rejection.
func rawHost(address string) string {
	if parsed, err := url.Parse(address); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	host, _, found := strings.Cut(address, ":")
	if !found {
		return address
	}
	return host
}
