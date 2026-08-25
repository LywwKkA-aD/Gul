package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An older v1 document has no "update" key at all. Its absence means exactly
// what the default means - nothing dismissed - which is why this section
// needed no schema bump.
func TestLoadWithoutUpdateKeyDismissesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	doc := `{"version":1,"connection":{"last_address":"old.example:64738","last_username":"gul"}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Update.DismissedVersion != "" {
		t.Errorf("dismissed_version = %q, want empty", cfg.Update.DismissedVersion)
	}
}

func TestUpdateRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := Defaults()
	cfg.Update.DismissedVersion = "v0.4.0-alpha.1"
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Update.DismissedVersion != "v0.4.0-alpha.1" {
		t.Errorf("dismissed_version = %q", got.Update.DismissedVersion)
	}
}

func TestUpdateSanitized(t *testing.T) {
	t.Parallel()
	if got := (Update{DismissedVersion: "  v0.4.0  "}).sanitized(); got.DismissedVersion != "v0.4.0" {
		t.Errorf("trimmed = %q", got.DismissedVersion)
	}
	// Truncating would leave a different, plausible version behind, and that
	// one would silence releases nobody dismissed.
	long := Update{DismissedVersion: strings.Repeat("9", maxDismissedLen+1)}
	if got := long.sanitized(); got.DismissedVersion != "" {
		t.Errorf("oversized dismissal = %q, want dropped", got.DismissedVersion)
	}
}
