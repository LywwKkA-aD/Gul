//go:build linux && (!cgo || server)

package hotkey

// noKeyStateToggleReason covers a build that can register a shortcut but has
// no way to read key state, such as a Linux build without cgo.
const noKeyStateToggleReason = "Нет доступа к состоянию клавиш, поэтому доступен только режим переключения: " +
	"нажатие включает передачу, повторное — выключает."

// Without cgo there is no Xlib, and a server build has no session to read keys
// from in the first place. The portal toggle still works when a registrar is
// wired in, because that path is pure Go.
func newMonitor(opts Options) (Monitor, error) {
	if isWaylandSession() {
		return newToggleMonitor(opts.Registrar, waylandToggleReason), nil
	}
	if opts.Registrar != nil {
		return newToggleMonitor(opts.Registrar, noKeyStateToggleReason), nil
	}
	return newUnsupported("Нет доступа к состоянию клавиш: сборка без поддержки X11."), nil
}
