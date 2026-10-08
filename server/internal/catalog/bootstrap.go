package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// BootstrapOwnerFile durably exports the personal key before publishing its
// hashed server identity. Both files are private; neither is ever replaced.
func BootstrapOwnerFile(path, output string) error {
	if output == path || !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return unavailable
	}
	dir, err := os.Lstat(filepath.Dir(output))
	if err != nil || !dir.IsDir() || dir.Mode().Perm() != 0700 {
		return unavailable
	}
	lock, err := lockPath(path)
	if err != nil {
		return unavailable
	}
	defer lock.Close()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return unavailable
	}
	key := OwnerKey{Format: "gul-member-key-v1", ServerID: RandomID(), MemberID: RandomID(), Credential: RandomCredential()}
	data, _ := json.Marshal(key)
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return unavailable
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(output)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return unavailable
	}
	if file.Sync() != nil || file.Close() != nil {
		return unavailable
	}
	directory, err := os.Open(filepath.Dir(output))
	if err != nil {
		return unavailable
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return unavailable
	}
	state := initialState(key)
	if state.Validate() != nil {
		return unavailable
	}
	if err := writeAtomic(path, state); err != nil {
		if _, exists := os.Lstat(path); exists == nil {
			keep = true
		}
		return unavailable
	}
	keep = true
	return nil
}
