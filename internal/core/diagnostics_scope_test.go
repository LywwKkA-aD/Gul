package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/config"
)

func TestAppCollectUsesConfiguredPreviewDirectory(t *testing.T) {
	// Fence the fallback in this test process too, so the failing version cannot
	// read or write the real installation's logs while reproducing the bug.
	legacyRoot := t.TempDir()
	t.Setenv("HOME", legacyRoot)
	t.Setenv("XDG_CONFIG_HOME", legacyRoot)
	t.Setenv("AppData", legacyRoot)
	legacyDir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "gul.log"), []byte("legacy log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previewDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(previewDir, "gul.log"), []byte("preview log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := New(discardLogger(), nil)
	app.UseSettings(previewDir, config.Defaults(), nil)
	path, err := app.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != previewDir {
		t.Fatalf("diagnostics escaped configured preview directory: %q", path)
	}
	if got := readZip(t, path)["logs/gul.log"]; got != "preview log\n" {
		t.Fatalf("archived wrong installation's log: %q", got)
	}
	archives, err := filepath.Glob(filepath.Join(legacyDir, diagnosticsPrefix+"*.zip"))
	if err != nil || len(archives) != 0 {
		t.Fatalf("preview diagnostics wrote to legacy installation: %v, %v", archives, err)
	}
}
