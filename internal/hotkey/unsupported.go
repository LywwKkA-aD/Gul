package hotkey

import (
	"fmt"
	"runtime"
)

// unsupportedMonitor stands in wherever no key state can be read. It exists so
// that New always hands back something with a Capability to render: a nil
// Monitor would push that branch into every caller.
type unsupportedMonitor struct {
	capability Capability
}

func newUnsupported(reason string) Monitor {
	return unsupportedMonitor{capability: Capability{Mode: ModeUnsupported, Reason: reason}}
}

func (m unsupportedMonitor) Capability() Capability { return m.capability }

// Watch never starts anything, and still separates a misspelled key from an
// unsupported platform: the caller's key name may be wrong on both counts and
// only one of them is worth reporting to the user as a platform limit.
func (m unsupportedMonitor) Watch(key string, onChange func(held bool)) error {
	if onChange == nil {
		return fmt.Errorf("hotkey: Watch needs a callback")
	}
	if !validKey(key) {
		return fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	return fmt.Errorf("%w on %s", ErrUnsupported, runtime.GOOS)
}

func (m unsupportedMonitor) Stop() {}
