//go:build darwin && !cgo

package hotkey

// CGEventSourceKeyState is reachable only through cgo. A cgo-less macOS build
// is not one this application ships, but it must still compile.
func newMonitor(_ Options) (Monitor, error) {
	return newUnsupported("Глобальная клавиша недоступна: сборка без cgo."), nil
}
