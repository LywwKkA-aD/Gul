package config

import (
	"errors"
	"testing"
)

// A schema bump without a migration step turns every existing installation
// into "defaults, file left alone" - silently, and only on the user's machine.
func TestMigrationChainCoversEveryOlderVersion(t *testing.T) {
	t.Parallel()
	for version := 0; version < SchemaVersion; version++ {
		if _, ok := migrations[version]; !ok {
			t.Errorf("no migration from version %d to %d", version, version+1)
		}
	}
	if err := migrate(map[string]any{}, 0, migrations); err != nil {
		t.Errorf("migrate from 0: %v", err)
	}
}

func TestMigrateReportsAGapInTheChain(t *testing.T) {
	t.Parallel()
	if err := migrate(map[string]any{}, 0, map[int]migration{}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want %v", err, ErrUnsupportedVersion)
	}
}

// Migrating up to the current version must not invent fields: the decoder
// fills what is missing from the defaults, and the step only reshapes.
func TestMigrationLeavesUnknownFieldsAlone(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"appearance": map[string]any{"accent": "#2F52DE"}}
	if err := migrate(doc, 0, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appearance, ok := doc["appearance"].(map[string]any)
	if !ok || appearance["accent"] != "#2F52DE" {
		t.Fatalf("document = %v", doc)
	}
}
