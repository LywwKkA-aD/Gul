//go:build !darwin && !windows && !linux

package hotkey

// No key-state backend exists for this platform. New still returns a Monitor
// so the caller has a Capability to show.
func newMonitor(_ Options) (Monitor, error) {
	return newUnsupported("Глобальная клавиша недоступна на этой платформе."), nil
}
