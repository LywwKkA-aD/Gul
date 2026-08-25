//go:build !darwin && !windows && !linux

package secret

// No credential store is known for this platform. The caller still gets a
// Store, reports Available false, and remembers servers without passwords.
func newStore(string) Store {
	return unavailable{reason: "no credential store on this platform"}
}
