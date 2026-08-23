package mumble

import (
	"net/url"
	"strings"
)

// redactedServer replaces every spelling of the server the user connected to.
const redactedServer = "<server>"

// RedactServer removes the connection arguments from text.
//
// gul.log ends up inside a diagnostics archive the user is expected to hand to
// somebody else (PLAN.md §10.7), so the server the user typed must not survive
// into a log record - not as the configured address, and not as the host:port
// pair that network errors embed on their own.
func RedactServer(text, address string) string {
	if text == "" || address == "" {
		return text
	}
	for _, spelling := range serverSpellings(address) {
		text = strings.ReplaceAll(text, spelling, redactedServer)
	}
	return text
}

// serverSpellings lists what has to disappear, longest first so a full URL is
// replaced as a whole rather than leaving its scheme and path behind.
func serverSpellings(address string) []string {
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
