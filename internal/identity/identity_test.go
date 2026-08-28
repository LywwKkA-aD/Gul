package identity

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

// testMaster is a fixed secret, so the vectors below are vectors and not
// whatever this run happened to generate.
var testMaster = func() []byte {
	seed := make([]byte, SeedBytes)
	for i := range seed {
		seed[i] = byte(i)
	}
	return seed
}()

// The golden vector, and the whole design resting on it.
//
// Murmur names a user by SHA-1 over the entire DER of the certificate it was
// shown - serial, dates, signature and all. The client works that value out
// before it connects and the relay rebuilds the same certificate from the seed
// it was sent, so the two must produce the same bytes on different machines,
// different builds and different days.
//
// The number below was measured, not chosen. If it moves, one of the fixed
// inputs stopped being fixed - a clock crept into the template, a field
// changed, or x509.CreateCertificate itself changed - and every user's name on
// every server changes with it. That is what this test is for; do not update
// it to whatever the code now produces without knowing which of those it was.
func TestTheIdentityOfASeedNeverMoves(t *testing.T) {
	t.Parallel()
	const (
		wantFingerprint = "2d5ccd27eecee2b5a587d975fdf138db1573c478"
		wantDERBytes    = 251
	)

	id, err := ForHost(testMaster, "murmur.example.test")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	der := id.Certificate.Certificate[0]
	if len(der) != wantDERBytes {
		t.Errorf("DER is %d bytes, want %d", len(der), wantDERBytes)
	}
	if id.Fingerprint != wantFingerprint {
		t.Errorf("fingerprint = %s, want %s", id.Fingerprint, wantFingerprint)
	}
	// And the fingerprint really is what Murmur will compute, not a value this
	// package agrees with only itself about.
	sum := sha1.Sum(der)
	if got := hex.EncodeToString(sum[:]); got != id.Fingerprint {
		t.Errorf("fingerprint %s is not SHA-1 of the DER (%s)", id.Fingerprint, got)
	}
}

// Two derivations of the same thing are the same bytes. Anything that read a
// clock or the random source would fail here, and would otherwise fail in
// production as a user whose name changed between two connections.
func TestDerivingTwiceGivesTheSameCertificate(t *testing.T) {
	t.Parallel()
	first, err := ForHost(testMaster, "murmur.example.test")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	second, err := ForHost(testMaster, "murmur.example.test")
	if err != nil {
		t.Fatalf("derive again: %v", err)
	}

	if !bytes.Equal(first.Certificate.Certificate[0], second.Certificate.Certificate[0]) {
		t.Fatal("the same seed produced two different certificates; the user's name would change between connections")
	}
}

// The client derives from the master and the relay from the seed it was sent.
// If those two paths disagree, the user is one person to their own client and
// another to the server, and nothing says so.
func TestTheRelayRebuildsWhatTheClientDerived(t *testing.T) {
	t.Parallel()
	const host = "murmur.example.test"
	mine, err := ForHost(testMaster, host)
	if err != nil {
		t.Fatalf("client derive: %v", err)
	}

	seed, err := HostSeed(testMaster, host)
	if err != nil {
		t.Fatalf("host seed: %v", err)
	}
	theirs, err := FromHostSeed(seed)
	if err != nil {
		t.Fatalf("relay derive: %v", err)
	}

	if theirs.Fingerprint != mine.Fingerprint {
		t.Fatalf("the relay would present %s, the client expects %s", theirs.Fingerprint, mine.Fingerprint)
	}
}

// The containment, stated as a test: a seed is worth nothing on another
// server. This is the only thing standing between a compromised relay and
// being you everywhere.
func TestASeedIsUselessOnAnotherServer(t *testing.T) {
	t.Parallel()
	here, err := HostSeed(testMaster, "murmur.example.test")
	if err != nil {
		t.Fatalf("host seed: %v", err)
	}
	elsewhere, err := HostSeed(testMaster, "other.example.test")
	if err != nil {
		t.Fatalf("host seed: %v", err)
	}

	if bytes.Equal(here, elsewhere) {
		t.Fatal("one seed serves two servers; a relay holding it could be this user anywhere")
	}

	mineElsewhere, err := ForHost(testMaster, "other.example.test")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	stolen, err := FromHostSeed(here)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if stolen.Fingerprint == mineElsewhere.Fingerprint {
		t.Fatal("a seed from one server names the same person on another")
	}
}

// Two people are two people, and a secret of the wrong size is refused rather
// than padded into one that looks fine.
func TestSeedsAreCheckedAndDistinct(t *testing.T) {
	t.Parallel()
	other := bytes.Clone(testMaster)
	other[0] ^= 1
	mine, err := ForHost(testMaster, "murmur.example.test")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	theirs, err := ForHost(other, "murmur.example.test")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if mine.Fingerprint == theirs.Fingerprint {
		t.Fatal("two masters gave one identity")
	}

	for name, seed := range map[string][]byte{
		"empty": {},
		"short": make([]byte, SeedBytes-1),
		"long":  make([]byte, SeedBytes+1),
	} {
		if _, err := FromHostSeed(seed); err == nil {
			t.Errorf("a %s seed was accepted", name)
		}
		if _, err := HostSeed(seed, "murmur.example.test"); err == nil {
			t.Errorf("a %s master was accepted", name)
		}
	}
	if _, err := HostSeed(testMaster, ""); err == nil {
		t.Error("a seed was derived without a server to scope it to")
	}
}
