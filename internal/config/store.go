package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

// FileName is the settings document inside the configuration directory.
const FileName = "config.json"

// ErrUnsupportedVersion reports a document this build must not touch: written
// by a newer version, or carrying a version field that is not a version at
// all. Both cases keep the file exactly as it is - overwriting it would cost
// the user the settings of the build that wrote it.
var ErrUnsupportedVersion = errors.New("unsupported settings schema version")

// ErrUnreadable reports a document that exists but could not be read
// (permissions, I/O). Nothing is known about its contents, so the caller
// must not overwrite it either: a later Save would replace settings that
// may be perfectly good with defaults.
var ErrUnreadable = errors.New("settings file is unreadable")

// Load reads the settings document from dir.
//
// It always returns a configuration that is safe to run on: defaults when
// there is nothing to read, and a sanitized document otherwise. A non-nil
// error says what was lost and is meant for a Warn - the caller keeps running
// on what it got back. The file itself is left untouched, with one exception:
// a document that is not a JSON object at all is renamed aside, because
// nothing can be recovered from it and it would otherwise be re-read on every
// start until a Save replaced it.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Defaults(), nil
	case err != nil:
		return Defaults(), fmt.Errorf("%w: read %s: %w", ErrUnreadable, FileName, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		quarantined, renameErr := quarantine(path)
		if renameErr != nil {
			return Defaults(), fmt.Errorf("%s is damaged and could not be moved aside: %w", FileName, renameErr)
		}
		return Defaults(), fmt.Errorf("%s is damaged, kept as %s: %w", FileName, filepath.Base(quarantined), err)
	}

	version, err := documentVersion(doc)
	if err != nil {
		return Defaults(), err
	}
	if version > SchemaVersion {
		return Defaults(), fmt.Errorf("%w: document is version %d, this build knows %d",
			ErrUnsupportedVersion, version, SchemaVersion)
	}
	if err := migrate(doc, version, migrations); err != nil {
		return Defaults(), err
	}

	migrated, err := json.Marshal(doc)
	if err != nil {
		return Defaults(), fmt.Errorf("re-encode %s: %w", FileName, err)
	}

	// Decoding over the defaults is the merge: a field the document does not
	// carry keeps the value a fresh installation would have.
	cfg := Defaults()
	decodeErr := json.Unmarshal(migrated, &cfg)
	cfg.extra = doc
	cfg = cfg.Sanitized()
	if decodeErr != nil {
		// A field of the wrong type is skipped by the decoder and everything
		// around it survives, so this is a report, not a reason to discard
		// the rest of the user's settings.
		return cfg, fmt.Errorf("%s has a damaged field: %w", FileName, decodeErr)
	}
	return cfg, nil
}

// Save writes cfg into dir atomically: a temporary file next to the target,
// flushed and renamed over it, so a crash mid-write leaves either the previous
// document or the new one. The file is 0600 and the directory 0700 - the
// document carries a server address and a nickname.
func Save(dir string, cfg Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := cfg.Sanitized().document()
	if err != nil {
		return err
	}

	// CreateTemp opens with 0600, which the rename carries over to the target.
	tmp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp settings file: %w", err)
	}
	tmpName := tmp.Name()
	written := false
	defer func() {
		if !written {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp settings file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("flush temp settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp settings file: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, FileName)); err != nil {
		return fmt.Errorf("replace %s: %w", FileName, err)
	}
	written = true
	return nil
}

// documentVersion reads the schema version of a document. A missing field is
// version 0: everything written before the schema was numbered.
func documentVersion(doc map[string]any) (int, error) {
	raw, ok := doc["version"]
	if !ok {
		return 0, nil
	}
	number, ok := raw.(float64)
	if !ok || number != math.Trunc(number) || number < 0 || number > math.MaxInt32 {
		return 0, fmt.Errorf("%w: version field is %v", ErrUnsupportedVersion, raw)
	}
	return int(number), nil
}

// quarantine moves a document that cannot be parsed out of the way, keeping
// it for the user, and returns its new path.
func quarantine(path string) (string, error) {
	broken := fmt.Sprintf("%s.broken-%s", path, time.Now().UTC().Format("20060102-150405"))
	if err := os.Rename(path, broken); err != nil {
		return "", err
	}
	return broken, nil
}
