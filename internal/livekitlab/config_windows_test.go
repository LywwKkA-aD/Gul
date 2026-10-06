//go:build windows

package livekitlab

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows mode bits do not represent NTFS ACLs. Until the local broker has an
// explicit ACL implementation, neither operation may access its credentials.
func TestConfigFilesUnsupportedOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	if err := Initialize(dir); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("initialization must reject native Windows: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initialization touched the config directory: %v", err)
	}
	if _, err := LoadConfig(filepath.Join(dir, "broker.json")); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("loading must reject native Windows before reading a file: %v", err)
	}
}
