// Package config owns the application configuration directory and the
// settings document persisted in it.
package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SchemaVersion is the version of the settings document this build writes.
// Every change to the shape of what Save produces needs a bump here and a step
// in the migration table (migrate.go).
const SchemaVersion = 1

const (
	// MaxAddressLen and MaxUsernameLen bound what may be remembered and what
	// may be dialled. They live here so the connect form, the validator in
	// core and the document on disk agree on one limit.
	MaxAddressLen  = 255
	MaxUsernameLen = 64
)

// Config is the persisted settings document. It is a value: callers hold
// snapshots, and the App owns the one that is written.
type Config struct {
	Version    int        `json:"version"`
	Connection Connection `json:"connection"`
	Audio      Audio      `json:"audio"`
	Gate       Gate       `json:"gate"`

	// extra is the document as it was read, so fields written by a build that
	// knows more of this schema version than we do survive a round trip
	// through this one. Read-only after Load: a snapshot shares it.
	extra map[string]any
}

// Connection is what the connect form starts on. The password is never part
// of it: it lives in the form for exactly one attempt.
type Connection struct {
	LastAddress  string `json:"last_address"`
	LastUsername string `json:"last_username"`
}

// Audio holds the device selection as the engine reports device ids: opaque
// hex strings, empty meaning the system default.
type Audio struct {
	CaptureID  string `json:"capture_id"`
	PlaybackID string `json:"playback_id"`
}

// Defaults returns the settings a fresh installation starts with.
func Defaults() Config {
	return Config{
		Version: SchemaVersion,
		Gate:    defaultGate(),
	}
}

// Sanitized folds a document into what the rest of the application accepts:
// ranges are clamped, values that carry no meaning fall back to the default,
// and a remembered connection that could not be dialled is forgotten. Applied
// on load, and again on every mutation, so nothing hand-edited reaches the
// engine.
func (c Config) Sanitized() Config {
	c.Version = SchemaVersion
	c.Connection = c.Connection.sanitized()
	c.Gate = c.Gate.sanitized()
	return c
}

func (c Connection) sanitized() Connection {
	c.LastAddress = strings.TrimSpace(c.LastAddress)
	c.LastUsername = strings.TrimSpace(c.LastUsername)
	// Truncating either field would produce a plausible-looking address or
	// nickname the user never typed, so an impossible one is dropped whole.
	if len(c.LastAddress) > MaxAddressLen {
		c.LastAddress = ""
	}
	if utf8.RuneCountInString(c.LastUsername) > MaxUsernameLen {
		c.LastUsername = ""
	}
	return c
}

// document renders the configuration as it is written to disk: the known
// fields over whatever the file already held.
func (c Config) document() ([]byte, error) {
	known, err := toDocument(c)
	if err != nil {
		return nil, err
	}
	merged := mergeDocument(cloneDocument(c.extra), known)
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return append(data, '\n'), nil
}

// toDocument turns the known fields into a generic document, so they can be
// merged over the preserved one without a second spelling of the schema.
func toDocument(c Config) (map[string]any, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return doc, nil
}

// mergeDocument writes src over dst, key by key, recursing into objects that
// exist on both sides. Keys only dst has are left untouched - that is what
// keeps an unknown field alive.
func mergeDocument(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any, len(src))
	}
	for key, value := range src {
		nested, isObject := value.(map[string]any)
		existing, wasObject := dst[key].(map[string]any)
		if isObject && wasObject {
			dst[key] = mergeDocument(existing, nested)
			continue
		}
		dst[key] = value
	}
	return dst
}

// cloneDocument copies a document deeply enough that merging into it cannot
// reach the snapshot it came from.
func cloneDocument(doc map[string]any) map[string]any {
	if doc == nil {
		return nil
	}
	out := make(map[string]any, len(doc))
	for key, value := range doc {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneDocument(nested)
			continue
		}
		out[key] = value
	}
	return out
}
