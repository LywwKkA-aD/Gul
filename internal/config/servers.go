package config

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Remembered servers. The connect form is a picker over this list, so the
// user chooses a server instead of retyping one.
//
// Note on the schema version: "servers" is a new field whose absence means
// exactly what its default means - a client that has never connected has
// nothing to pick from - so it does not need a SchemaVersion bump under the
// rule config.go documents. Load decodes over Defaults(), and a v1 document
// written by an older build simply comes back with an empty list that the
// first successful connect fills. The list is deliberately not seeded from
// connection.last_address on load: seeding would make the absence of the
// field mean something other than its default, which is precisely the change
// that would require a bump.

// MaxServers caps the picker. Eight is well past the number of servers a
// person actually uses and keeps the list a glance rather than a scroll.
const MaxServers = 8

// Server is one remembered server. The password is NOT here and never will
// be: it lives in the operating system's credential store (internal/secret),
// keyed by this Address. config.json is a plain document that travels in
// backups and diagnostics archives.
type Server struct {
	Address  string `json:"address"`
	Username string `json:"username"`
	// LastUsed is unix seconds, and orders the picker. It is an ordering
	// hint, not a log: a nonsensical value costs the entry its place in the
	// list, not the entry itself.
	LastUsed int64 `json:"last_used"`
}

// RememberServer records a server the user has just connected to, and returns
// the new list. The caller passes the current time; nothing here reads a
// clock, so the ordering is testable.
//
// One entry per address: a repeat connect updates the username and the
// timestamp in place rather than appending a second row for the same server.
// The address is the identity, and it is matched exactly (after trimming),
// because the same string is the key into the credential store - folding case
// here would let one spelling of a host read another's password.
//
// An address or username the document would not accept is not remembered; the
// list comes back sanitized either way.
func RememberServer(list []Server, address, username string, at time.Time) []Server {
	entry := Server{
		Address:  strings.TrimSpace(address),
		Username: strings.TrimSpace(username),
		LastUsed: at.Unix(),
	}
	if !entry.usable() {
		return sanitizeServers(list)
	}

	// last_used orders the picker. A clock that went backwards between two
	// connects would otherwise bury the server the user is on right now
	// underneath one they have not touched in a month, so the new entry is
	// never stamped older than what it is being put in front of.
	kept := make([]Server, 0, len(list)+1)
	for _, s := range list {
		if strings.TrimSpace(s.Address) == entry.Address {
			continue
		}
		if s.LastUsed > entry.LastUsed {
			entry.LastUsed = s.LastUsed
		}
		kept = append(kept, s)
	}
	return sanitizeServers(append([]Server{entry}, kept...))
}

// ForgetServer drops the entry for address and returns the new list. Removing
// the password that goes with it is the caller's job - this package does not
// know the credential store exists.
func ForgetServer(list []Server, address string) []Server {
	address = strings.TrimSpace(address)
	kept := make([]Server, 0, len(list))
	for _, s := range list {
		if strings.TrimSpace(s.Address) == address {
			continue
		}
		kept = append(kept, s)
	}
	return sanitizeServers(kept)
}

// sanitizeServers folds a list into what the picker accepts: trimmed, newest
// first, one entry per address, capped at MaxServers, and free of entries no
// connect could be made from.
//
// It always returns a fresh slice, never a re-slice of its input: Config is a
// value that callers hold snapshots of, and writing through a shared backing
// array would let a mutation reach a snapshot taken before it.
func sanitizeServers(list []Server) []Server {
	out := make([]Server, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, s := range list {
		s.Address = strings.TrimSpace(s.Address)
		s.Username = strings.TrimSpace(s.Username)
		if s.LastUsed < 0 {
			// Only the ordering is nonsense; the address and nickname are
			// still perfectly good to connect with.
			s.LastUsed = 0
		}
		if !s.usable() {
			// A hand-edited or truncated entry is dropped rather than
			// failing the load: the rest of the settings are fine.
			continue
		}
		if _, dup := seen[s.Address]; dup {
			continue
		}
		seen[s.Address] = struct{}{}
		out = append(out, s)
	}

	// Stable, so entries stamped in the same second keep the order they were
	// given - which is what puts a just-remembered server at the top.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastUsed > out[j].LastUsed })
	if len(out) > MaxServers {
		out = out[:MaxServers]
	}
	return out
}

// usable reports whether this entry could be dialled as it stands. The bounds
// are the document's own (MaxAddressLen, MaxUsernameLen), so what may be
// remembered is exactly what may be typed into the connect form.
func (s Server) usable() bool {
	switch {
	case s.Address == "" || s.Username == "":
		// A row with nothing to dial or nobody to be is not a picker entry.
		return false
	case len(s.Address) > MaxAddressLen:
		return false
	case utf8.RuneCountInString(s.Username) > MaxUsernameLen:
		return false
	case hasControlRunes(s.Address) || hasControlRunes(s.Username):
		// Nothing typed into the form can produce these. A NUL in the
		// address would also silently truncate the credential-store key,
		// making two different servers share one password.
		return false
	}
	return true
}

func hasControlRunes(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
