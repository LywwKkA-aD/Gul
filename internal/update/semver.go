// Package update answers one question, once, at startup: is there a release
// newer than the one running? It never downloads anything and never asks the
// user for anything - a newer version is a line in the window, and the user
// installs it themselves (PLAN.md 7 M4+).
//
// Silence is the failure mode. No network, a rate limit, a shape GitHub does
// not owe us - all of them mean "nothing to report". A courtesy that nags is
// worse than no courtesy at all.
package update

import (
	"strconv"
	"strings"
)

// maxVersionLen bounds what may be parsed. Versions are short; anything long
// is a body that is not a version, and refusing it early keeps the comparison
// off unbounded input from the network.
const maxVersionLen = 128

// Version is a parsed semantic version (semver.org 2.0.0). Build metadata is
// parsed and then dropped: the specification says it takes no part in
// precedence, so keeping it would only invite a comparison that uses it.
type Version struct {
	Major, Minor, Patch uint64
	// Prerelease holds the dot-separated identifiers after "-", empty for a
	// final release. A release with any prerelease is OLDER than the same
	// numbers without one.
	Prerelease []string
}

// Parse reads a version, with or without the "v" a git tag carries.
//
// It is deliberately strict: three numeric components, optional prerelease,
// optional build metadata, nothing else. A tag this build cannot make sense of
// is not silently treated as an upgrade.
func Parse(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxVersionLen {
		return Version{}, false
	}
	s = strings.TrimPrefix(s, "v")

	// Build metadata first: it may contain hyphens, so cutting on "-" before
	// "+" would tear "1.0.0+build-7" apart.
	if plus := strings.IndexByte(s, '+'); plus >= 0 {
		build := s[plus+1:]
		if !validDotSeparated(build, true) {
			return Version{}, false
		}
		s = s[:plus]
	}

	var prerelease []string
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		pre := s[dash+1:]
		if !validDotSeparated(pre, false) {
			return Version{}, false
		}
		prerelease = strings.Split(pre, ".")
		s = s[:dash]
	}

	core := strings.Split(s, ".")
	if len(core) != 3 {
		return Version{}, false
	}
	var v Version
	numbers := [...]*uint64{&v.Major, &v.Minor, &v.Patch}
	for i, part := range core {
		n, err := parseNumericIdentifier(part)
		if err != nil {
			return Version{}, false
		}
		*numbers[i] = n
	}
	v.Prerelease = prerelease
	return v, true
}

// Compare orders two versions: -1 if a precedes b, +1 if it follows, 0 when
// they have the same precedence.
func Compare(a, b Version) int {
	if c := compareUint(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareUint(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareUint(a.Patch, b.Patch); c != 0 {
		return c
	}

	// "A pre-release version has lower precedence than the associated normal
	// version" - 0.4.0-alpha.1 comes before 0.4.0.
	switch {
	case len(a.Prerelease) == 0 && len(b.Prerelease) == 0:
		return 0
	case len(a.Prerelease) == 0:
		return 1
	case len(b.Prerelease) == 0:
		return -1
	}

	for i := 0; i < len(a.Prerelease) && i < len(b.Prerelease); i++ {
		if c := comparePrereleaseIdentifier(a.Prerelease[i], b.Prerelease[i]); c != 0 {
			return c
		}
	}
	// Everything shared is equal, so the longer prerelease wins:
	// "alpha" precedes "alpha.1".
	return compareInt(len(a.Prerelease), len(b.Prerelease))
}

// IsNewer reports whether candidate names a release that follows current.
// A version either side cannot parse is not newer: this is the guard that
// keeps a surprising tag from announcing an upgrade that does not exist.
func IsNewer(candidate, current string) bool {
	c, ok := Parse(candidate)
	if !ok {
		return false
	}
	cur, ok := Parse(current)
	if !ok {
		return false
	}
	return Compare(c, cur) > 0
}

// ShouldAnnounce decides whether latest earns the one line the UI gives it.
//
// dismissed is the version the user last told us to stop mentioning: that one
// stays quiet forever, and anything newer than it speaks up again. An empty
// dismissed means nothing has been dismissed yet.
func ShouldAnnounce(latest, current, dismissed string) bool {
	if !IsNewer(latest, current) {
		return false
	}
	if strings.TrimSpace(dismissed) == "" {
		return true
	}
	if _, ok := Parse(dismissed); !ok {
		// A dismissal this build cannot read silences nothing: forgetting a
		// dismissal shows one extra line, honouring a value we misread could
		// hide every future release.
		return true
	}
	return IsNewer(latest, dismissed)
}

// comparePrereleaseIdentifier orders two prerelease identifiers by the rules
// of semver 11.4: numeric identifiers compare numerically, alphanumeric ones
// compare by ASCII, and a numeric identifier always precedes an alphanumeric
// one.
func comparePrereleaseIdentifier(a, b string) int {
	an, aNum := parseIdentifierNumber(a)
	bn, bNum := parseIdentifierNumber(b)
	switch {
	case aNum && bNum:
		return compareUint(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// parseIdentifierNumber reports whether an identifier is numeric, and its
// value. An identifier too long to fit a uint64 is treated as alphanumeric
// rather than failing the whole parse: it still orders deterministically.
func parseIdentifierNumber(s string) (uint64, bool) {
	n, err := parseNumericIdentifier(s)
	return n, err == nil
}

// parseNumericIdentifier reads a numeric identifier: digits only, and no
// leading zero, which semver forbids so that "1.01.0" cannot exist alongside
// "1.1.0".
func parseNumericIdentifier(s string) (uint64, error) {
	if s == "" {
		return 0, strconv.ErrSyntax
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, strconv.ErrSyntax
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	return strconv.ParseUint(s, 10, 64)
}

// validDotSeparated checks a prerelease or build-metadata string: dot
// separated identifiers of [0-9A-Za-z-], none empty. Only a prerelease
// forbids leading zeroes in a numeric identifier (semver 11.4.1); build
// metadata has no ordering, so it does not care.
func validDotSeparated(s string, isBuild bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for i := 0; i < len(id); i++ {
			c := id[i]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				numeric = false
			default:
				return false
			}
		}
		if !isBuild && numeric && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
