package identity

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// seedFile is the master secret on disk. One file, one user, every server.
const seedFile = "identity.seed"

// Load reads the master secret, creating it on first run.
//
// The rules are the ones cert.go arrived at for the key it replaces, and they
// are the same rules for the same reasons:
//
//   - No file: make one. A new install is a new person, which is correct.
//   - A directory that cannot be written: run on a secret that lives for this
//     process only, and say so. Somebody with a read-only home should still be
//     able to talk; they will simply be a stranger every launch.
//   - A file that exists and is the wrong size: fail, and do not replace it.
//     This is the important one. Silently generating a new secret would rename
//     the user on every server at once, losing whatever they had - and the
//     cause would be a truncated write or a half-restored backup, which is
//     exactly the moment a program must not decide on its own to start over.
func Load(dir string, log *slog.Logger) ([]byte, error) {
	if log == nil {
		log = slog.Default()
	}
	path := filepath.Join(dir, seedFile)

	seed, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(seed) != SeedBytes {
			return nil, fmt.Errorf(
				"identity: %s is %d bytes, want %d - refusing to replace it, "+
					"because a new secret is a new identity on every server",
				path, len(seed), SeedBytes)
		}
		return seed, nil
	case !errors.Is(err, os.ErrNotExist):
		log.Warn("identity seed unreadable, using one that lasts this run only",
			"error", err)
		return ephemeral()
	}

	seed = make([]byte, SeedBytes)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	if err := writeSecret(path, seed); err != nil {
		log.Warn("identity seed could not be saved, using one that lasts this run only",
			"error", err)
	}
	return seed, nil
}

// ephemeral is the fallback identity: real for this process and gone with it.
func ephemeral() ([]byte, error) {
	seed := make([]byte, SeedBytes)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	return seed, nil
}

// writeSecret writes atomically with owner-only permissions. The explicit
// Chmod defends against a permissive process umask.
func writeSecret(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
