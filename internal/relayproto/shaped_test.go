package relayproto

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// recorder is the wire seen from outside: it remembers the size of every write,
// which is exactly what an observer of the outer TLS session sees, one record
// per write.
type recorder struct {
	mu     sync.Mutex
	sizes  []int
	body   bytes.Buffer
	closed bool
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, net.ErrClosed
	}
	r.sizes = append(r.sizes, len(p))
	return r.body.Write(p)
}

func (r *recorder) observed() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.sizes...)
}

func (r *recorder) Read([]byte) (int, error) { return 0, io.EOF }
func (r *recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}
func (r *recorder) LocalAddr() net.Addr              { return nil }
func (r *recorder) RemoteAddr() net.Addr             { return nil }
func (r *recorder) SetDeadline(time.Time) error      { return nil }
func (r *recorder) SetReadDeadline(time.Time) error  { return nil }
func (r *recorder) SetWriteDeadline(time.Time) error { return nil }

// This is the leak the shaping exists to close. Variable bitrate makes an Opus
// frame between 34 and 99 bytes, the size follows the energy of the speech, and
// on the plain contract that size is the length of a TLS record anyone on the
// path can measure. After shaping there must be exactly one size.
func TestEveryVoiceSizedWriteLeavesAtOneSize(t *testing.T) {
	t.Parallel()
	wire := &recorder{}
	shaped := Shape(wire)

	// The Opus spread, plus what the inner Mumble framing and the inner TLS
	// record add on top of it.
	for payload := 34; payload <= 99; payload++ {
		for _, overhead := range []int{22, 40, 60} {
			if _, err := shaped.Write(make([]byte, payload+overhead)); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}

	sizes := wire.observed()
	if len(sizes) == 0 {
		t.Fatal("nothing reached the wire")
	}
	unique := make(map[int]bool)
	for _, n := range sizes {
		unique[n] = true
	}
	if len(unique) != 1 {
		t.Fatalf("%d distinct sizes on the wire, want 1: the encoder still shows through %v", len(unique), unique)
	}
}

// The gap the grid left open, and the one that was actually being used.
//
// Rounding up to the cell hides a voice frame, which is 34 to 99 bytes, and
// that is what the shaping was written and tested for. It hides nothing about
// a record larger than a cell: an inner TLS handshake of 1300 to 1500 bytes
// came out as frames of 1536 and 1280, and those were the only two sizes in a
// whole session that were not 256. Measured on a live client, that is exactly
// where two users' connections stopped carrying anything - 2816 bytes, the two
// oversized frames, and then nothing ever again.
//
// One size means one size. Every frame is a cell, whatever was handed in.
func TestNoWriteEverLeavesLargerThanACell(t *testing.T) {
	t.Parallel()
	wire := &recorder{}
	shaped := Shape(wire)

	for _, n := range []int{
		1, 60, 99, // voice, which always fitted
		250, 251, 252, // the cell payload and either side of it
		1300, 1500, // the inner TLS handshake records
		4096, 1 << 15, // copy buffers, and the old sending bound
	} {
		if _, err := shaped.Write(make([]byte, n)); err != nil {
			t.Fatalf("write %d: %v", n, err)
		}
	}

	sizes := wire.observed()
	if len(sizes) == 0 {
		t.Fatal("nothing reached the wire")
	}
	for i, size := range sizes {
		if size != shapedBucket {
			t.Fatalf("frame %d left at %d bytes, want exactly one %d byte cell: "+
				"a size that stands out is the whole signature", i, size, shapedBucket)
		}
	}
}

// Splitting instead of rounding has to stay free. A record larger than a cell
// was already padded up to a multiple of the grid, so cutting it into cells
// puts the same bytes on the wire - and shaping that costs bandwidth is
// shaping somebody will want turned off.
func TestSplittingIntoCellsCostsNoBytes(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1300, 1500, 4096} {
		wire := &recorder{}
		if _, err := Shape(wire).Write(make([]byte, n)); err != nil {
			t.Fatalf("write %d: %v", n, err)
		}
		onWire := 0
		for _, size := range wire.observed() {
			onWire += size
		}
		body := shapedHeaderLen + n
		rounded := body + (shapedBucket-body%shapedBucket)%shapedBucket
		if onWire > rounded {
			t.Fatalf("%d bytes took %d on the wire, more than the %d one padded frame took",
				n, onWire, rounded)
		}
	}
}

// Chaff has to be the same size as speech, or filling the silences would
// simply replace one legible pattern with another.
func TestChaffIsIndistinguishableFromSpeechBySize(t *testing.T) {
	t.Parallel()
	wire := &recorder{}
	shaped := Shape(wire)

	if _, err := shaped.Write(make([]byte, 80)); err != nil {
		t.Fatalf("write: %v", err)
	}
	speech := wire.observed()[0]

	ctx, cancel := context.WithCancel(t.Context())
	go shaped.SendChaff(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for len(wire.observed()) < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	sizes := wire.observed()
	if len(sizes) < 4 {
		t.Fatalf("chaff produced %d writes in three seconds, want several", len(sizes)-1)
	}
	for i, n := range sizes {
		if n != speech {
			t.Fatalf("write %d is %d bytes, speech is %d: chaff has a size of its own", i, n, speech)
		}
	}
}

// Whatever goes in comes out, at any size and across any split. The frames are
// length-delimited precisely because the stream underneath does not preserve
// the boundaries a write had.
func TestShapedRoundTrip(t *testing.T) {
	t.Parallel()
	left, right := net.Pipe()
	a, b := Shape(left), Shape(right)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	payloads := [][]byte{
		[]byte("x"),
		bytes.Repeat([]byte("voice"), 20),
		bytes.Repeat([]byte("a"), shapedBucket-shapedHeaderLen), // exactly one cell
		bytes.Repeat([]byte("b"), shapedBucket),                 // one over
		bytes.Repeat([]byte("c"), 70_000),                       // more than one frame
	}
	go func() {
		for _, p := range payloads {
			if _, err := a.Write(p); err != nil {
				return
			}
		}
	}()

	for i, want := range payloads {
		got := make([]byte, len(want))
		if _, err := io.ReadFull(b, got); err != nil {
			t.Fatalf("payload %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("payload %d came back wrong (%d bytes)", i, len(got))
		}
	}
}

// Chaff must be invisible above the framing: a reader waiting for speech must
// not see an empty read, a short read, or anything at all until speech arrives.
func TestChaffNeverReachesTheReader(t *testing.T) {
	t.Parallel()
	left, right := net.Pipe()
	a, b := Shape(left), Shape(right)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go a.SendChaff(ctx)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = a.Write([]byte("speech"))
	}()

	got := make([]byte, 6)
	if _, err := io.ReadFull(b, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "speech" {
		t.Fatalf("read %q, want the speech that followed the chaff", got)
	}
}

// A frame the reader cannot account for is refused rather than skipped. Both
// ends agree on the format before a byte is sent, so anything else is a
// mismatch worth surfacing.
func TestShapedRefusesUnknownFrames(t *testing.T) {
	t.Parallel()
	tests := map[string][]byte{
		"unknown kind":             {0x7F, 0, 0, 0, 0},
		"payload beyond the bound": {shapedKindData, 0xFF, 0xFF, 0, 0},
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			left, right := net.Pipe()
			t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
			go func() { _, _ = left.Write(header) }()

			_, err := Shape(right).Read(make([]byte, 16))
			if err == nil {
				t.Fatal("the frame was accepted")
			}
			if errors.Is(err, io.EOF) {
				t.Fatalf("refused as a short read rather than a bad frame: %v", err)
			}
		})
	}
}

// The names come from the credential and differ from each other, or the two
// contracts could not be told apart in the handshake.
func TestShapedNameIsDerivedAndDistinct(t *testing.T) {
	t.Parallel()
	names := NamesFor(Derive([]byte("server secret")))
	other := NamesFor(Derive([]byte("another server")))

	if names.Shaped == "" || names.Shaped == names.Subprotocol {
		t.Fatalf("shaped name %q does not stand apart from the plain one %q", names.Shaped, names.Subprotocol)
	}
	if names.Shaped == other.Shaped {
		t.Fatal("two servers derived the same shaped name")
	}
}
