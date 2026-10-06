package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// LiveKitDir keeps local migration builds away from the existing Gul settings.
// An absolute override allows independent clients in the same local test.
func LiveKitDir(override string) (string, error) {
	dir := override
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir: %w", err)
		}
		dir = filepath.Join(base, "gul-livekit")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("LiveKit config directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create LiveKit config dir: %w", err)
	}
	return filepath.Clean(dir), nil
}
