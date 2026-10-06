package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveKitDirIsolatedOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "livekit", "client-a")
	got, err := LiveKitDir(want)
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	info, err := os.Stat(got)
	if err != nil || !info.IsDir() {
		t.Fatalf("missing client directory: %v", err)
	}
	if _, err := LiveKitDir("relative-client"); err == nil {
		t.Fatal("relative override accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LiveKitDir(filepath.Join(file, "client")); err == nil {
		t.Fatal("invalid directory accepted")
	}
}
