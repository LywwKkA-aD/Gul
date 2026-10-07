// Package broker serves authenticated Gul sessions and LiveKit media grants.
package broker

import (
	"errors"
	"strings"
	"unicode"
)

// credentials never cross the HTTP boundary or appear in diagnostics.
type credentials struct {
	APIKey    string
	APISecret string
}

func (c credentials) validate() error {
	if !validIdentity(c.APIKey) || len(c.APISecret) < 32 || len(c.APISecret) > 256 || strings.ContainsAny(c.APISecret, "\r\n\t ") {
		return errors.New("invalid LiveKit signing credentials")
	}
	return nil
}

func validIdentity(identity string) bool {
	if len(identity) == 0 || len(identity) > 64 {
		return false
	}
	for _, r := range identity {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_.", r) {
			return false
		}
	}
	return true
}
