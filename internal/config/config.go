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
//
// A bump here and a step in the migration table (migrate.go) are needed
// whenever an older document cannot be read correctly as it stands: a renamed
// or removed field, a changed meaning, or a new field whose absence is not
// the same as its default. Adding a field that Defaults() fills is not such a
// change - Load decodes over Defaults, so an older document simply gets the
// default (cue_volume and the servers list were both added this way, each
// with a test pinning it).
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
	// Servers is the remembered picker list, newest first (servers.go).
	// Never a password: those live in internal/secret, keyed by Address.
	Servers []Server `json:"servers"`

	// extra is the document as it was read, so fields written by a build that
	// knows more of this schema version than we do survive a round trip
	// through this one. Read-only after Load: a snapshot shares it.
	extra map[string]any
}

// Connection is what the connect form starts on, and stays the last-used
// pair even after the picker (Servers) was added. The password is never part
// of it: config.json holds no secrets at all, and a remembered password lives
// in the operating system's credential store instead (internal/secret).
type Connection struct {
	LastAddress  string `json:"last_address"`
	LastUsername string `json:"last_username"`
}

// Defaults returns the settings a fresh installation starts with.
func Defaults() Config {
	return Config{
		Version: SchemaVersion,
		Audio:   defaultAudio(),
		Gate:    defaultGate(),
	}
}

// Sanitized folds a document into what the rest of the application accepts:
// ranges are clamped, values that carry no meaning fall back to the default,
// and a remembered connection that could not be dialled is forgotten. Applied
// on load, and again on every mutation, so nothing hand-edited reaches the
// engine.
//
// Config is a value and callers hold snapshots of it, so the slice field is
// rebuilt rather than filtered in place - see sanitizeServers.
func (c Config) Sanitized() Config {
	c.Version = SchemaVersion
	c.Connection = c.Connection.sanitized()
	c.Audio = c.Audio.sanitized()
	c.Gate = c.Gate.sanitized()
	c.Servers = sanitizeServers(c.Servers)
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
