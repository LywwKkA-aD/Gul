//go:build darwin && !cgo

package secret

// The keychain is reachable only through Security.framework, i.e. only
// through cgo. A cgo-less macOS build is not one this application ships (the
// whole audio stack is cgo), but it must still compile - and it must degrade
// the way every other storeless machine does rather than fail to build.
func newStore(string) Store {
	return unavailable{reason: "macOS keychain needs a cgo build"}
}
