package identity

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The secret is made once and then kept. A second launch that generated a new
// one would rename the user on every server they use.
func TestTheSeedIsMadeOnceAndKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := Load(dir, quietLogger())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := Load(dir, quietLogger())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatal("the second launch got a different secret; the user would be a stranger on every server")
	}
	if len(first) != SeedBytes {
		t.Fatalf("seed is %d bytes, want %d", len(first), SeedBytes)
	}
	if runtime.GOOS != "windows" {
		// Unix permission bits are not meaningful on Windows, where the same
		// Chmod produces something else entirely. cert.go's test skips for the
		// same reason.
		info, err := os.Stat(filepath.Join(dir, seedFile))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("seed file mode = %v, want owner-only", perm)
		}
	}
}

// A truncated or half-restored file is refused rather than replaced. Replacing
// it would lose the user's name everywhere at once, and the cause would be an
// accident rather than a decision.
func TestATruncatedSeedIsRefusedRatherThanReplaced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, seedFile)
	if err := os.WriteFile(path, make([]byte, SeedBytes-4), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Load(dir, quietLogger()); err == nil {
		t.Fatal("a short seed was accepted or silently replaced")
	}

	// And it is still there, untouched, for somebody to restore from a backup.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != SeedBytes-4 {
		t.Fatalf("the file was rewritten: %d bytes", len(got))
	}
}

// A home directory that cannot be written costs the identity its lifetime,
// never the connection.
func TestAnUnwritableDirectoryStillYieldsAnIdentity(t *testing.T) {
	t.Parallel()
	seed, err := Load(filepath.Join(t.TempDir(), "does", "not", "exist"), quietLogger())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(seed) != SeedBytes {
		t.Fatalf("seed is %d bytes, want %d", len(seed), SeedBytes)
	}
	if _, err := ForHost(seed, "murmur.example.test"); err != nil {
		t.Fatalf("the fallback secret does not yield an identity: %v", err)
	}
}
