//go:build !darwin && !windows && !linux

package hotkey

// No key table exists for this platform; the shared table test skips on a nil
// mapping rather than pretending the vocabulary is covered.
func platformTable() (mapped map[string]keyCode, unmapped map[string]string) {
	return nil, nil
}
