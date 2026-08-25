package config

import "strings"

// The startup version check (internal/update) remembers exactly one thing
// between runs: the release the user has told us to stop mentioning.
//
// Note on the schema version: "update" is a new section whose absence means
// exactly what its default means - nothing has been dismissed yet - so it does
// not need a SchemaVersion bump under the rule config.go documents.

// maxDismissedLen bounds the stored tag. It matches the bound the version
// parser applies, so a value this document accepts is a value that package
// can still read.
const maxDismissedLen = 128

// Update is the update section of the settings document.
type Update struct {
	// DismissedVersion is the newest release the user has dismissed. That
	// release stays quiet forever; anything newer speaks up again. Empty
	// means nothing has been dismissed.
	DismissedVersion string `json:"dismissed_version"`
}

// sanitized folds a hand-edited section into what the check accepts. A value
// too long to be a version is dropped rather than truncated: a truncated tag
// is a different, plausible-looking version that would silence releases the
// user never dismissed.
func (u Update) sanitized() Update {
	u.DismissedVersion = strings.TrimSpace(u.DismissedVersion)
	if len(u.DismissedVersion) > maxDismissedLen {
		u.DismissedVersion = ""
	}
	return u
}
