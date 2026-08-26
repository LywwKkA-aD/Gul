package relayproto

import (
	"bytes"
	"testing"
)

func testObfuscator(secret string) *Obfuscator {
	return NewObfuscator(Derive([]byte(secret)))
}

func TestObfuscatorRoundTrip(t *testing.T) {
	t.Parallel()
	o := testObfuscator("server password")
	want := []byte{0xc0, 0x00, 0x00, 0x00, 0x01, 0x08, 0xde, 0xad, 0xbe, 0xef}

	scrambled := make([]byte, len(want)+o.Overhead())
	n := o.Obfuscate(want, scrambled)
	if n != len(want)+o.Overhead() {
		t.Fatalf("obfuscated length = %d, want %d", n, len(want)+o.Overhead())
	}
	got := make([]byte, len(want))
	if m := o.Deobfuscate(scrambled[:n], got); m != len(want) {
		t.Fatalf("deobfuscated length = %d, want %d", m, len(want))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip = %x, want %x", got, want)
	}
}

// The point of the exercise: what leaves must not carry the shape of what went
// in. A QUIC Initial starts with a recognisable header and a version number,
// and that is exactly what a network keys on.
func TestObfuscatorLeavesNoQUICHeader(t *testing.T) {
	t.Parallel()
	o := testObfuscator("server password")
	// A QUIC v1 long header: first byte 0b11xxxxxx, then the version.
	packet := append([]byte{0xc3, 0x00, 0x00, 0x00, 0x01}, bytes.Repeat([]byte{0x00}, 60)...)

	scrambled := make([]byte, len(packet)+o.Overhead())
	n := o.Obfuscate(packet, scrambled)
	body := scrambled[o.Overhead():n]

	if body[0]&0xc0 == 0xc0 && bytes.Equal(body[1:5], []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Fatal("the QUIC version survived scrambling")
	}
	// Sixty zero bytes must not come out as sixty identical bytes either.
	tail := body[5:]
	same := true
	for _, b := range tail {
		if b != tail[0] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("a run of identical bytes stayed a run; the keystream does not vary")
	}
}

// Two datagrams with the same contents must not look alike, or a network could
// match them without understanding either.
func TestObfuscatorVariesPerPacket(t *testing.T) {
	t.Parallel()
	o := testObfuscator("server password")
	packet := bytes.Repeat([]byte{0x42}, 40)

	first := make([]byte, len(packet)+o.Overhead())
	second := make([]byte, len(packet)+o.Overhead())
	o.Obfuscate(packet, first)
	o.Obfuscate(packet, second)

	if bytes.Equal(first, second) {
		t.Fatal("the same datagram scrambled to the same bytes twice")
	}
}

// Somebody without the password gets nothing back, and what they send comes
// out as noise rather than as anything QUIC would answer.
func TestObfuscatorRejectsAnotherKey(t *testing.T) {
	t.Parallel()
	mine := testObfuscator("server password")
	theirs := testObfuscator("some other password")
	packet := []byte{0xc0, 0x00, 0x00, 0x00, 0x01, 0xaa, 0xbb}

	scrambled := make([]byte, len(packet)+mine.Overhead())
	n := mine.Obfuscate(packet, scrambled)
	got := make([]byte, len(packet))
	theirs.Deobfuscate(scrambled[:n], got)

	if bytes.Equal(got, packet) {
		t.Fatal("the wrong key recovered the packet")
	}
}

// Short and malformed datagrams must be dropped, not misread: a UDP port on
// the public internet is offered a great deal of both.
func TestObfuscatorDropsWhatCannotBeUnscrambled(t *testing.T) {
	t.Parallel()
	o := testObfuscator("server password")
	out := make([]byte, 64)
	for _, size := range []int{0, 1, salamanderSaltLen} {
		if n := o.Deobfuscate(make([]byte, size), out); n != 0 {
			t.Errorf("a %d-byte datagram yielded %d bytes", size, n)
		}
	}
	// And a caller whose buffer is too small is told, rather than truncated.
	if n := o.Obfuscate(make([]byte, 32), make([]byte, 8)); n != 0 {
		t.Errorf("scrambled into a short buffer: %d", n)
	}
}

func BenchmarkObfuscate(b *testing.B) {
	o := testObfuscator("server password")
	packet := bytes.Repeat([]byte{0x42}, 1200)
	out := make([]byte, len(packet)+o.Overhead())
	b.SetBytes(int64(len(packet)))
	b.ReportAllocs()
	for b.Loop() {
		o.Obfuscate(packet, out)
	}
}
