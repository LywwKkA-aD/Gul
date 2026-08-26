package relayproto

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestQUICPreambleRoundTrip(t *testing.T) {
	t.Parallel()
	want := Derive([]byte("server password"))
	var buffer bytes.Buffer
	if err := WriteQUICPreamble(&buffer, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadQUICPreamble(&buffer)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("credential = %q, want %q", got, want)
	}
	if buffer.Len() != 0 {
		t.Fatalf("%d bytes left over; the preamble must not eat the tunnel", buffer.Len())
	}
}

// The length is read before the body, so a peer must not be able to announce
// a size the relay would then allocate.
func TestQUICPreambleRefusesAnImpossibleLength(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"zero":           {0x00, 0x00},
		"over the bound": {0xff, 0xff},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadQUICPreamble(bytes.NewReader(header)); err == nil {
				t.Fatal("an impossible length was accepted")
			}
		})
	}
	if err := WriteQUICPreamble(io.Discard, Credential(strings.Repeat("x", quicPreambleMax+1))); err == nil {
		t.Fatal("an oversized credential was written")
	}
	if err := WriteQUICPreamble(io.Discard, ""); err == nil {
		t.Fatal("an empty credential was written")
	}
}

// A connection that stops halfway has to fail, not block or half-succeed.
func TestQUICPreambleRefusesATruncatedStream(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := WriteQUICPreamble(&buffer, Derive([]byte("server password"))); err != nil {
		t.Fatalf("write: %v", err)
	}
	truncated := buffer.Bytes()[:buffer.Len()-4]
	if _, err := ReadQUICPreamble(bytes.NewReader(truncated)); err == nil {
		t.Fatal("a truncated preamble was accepted")
	} else if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want an EOF", err)
	}
}

// The protocol name is what a QUIC Initial packet shows in the clear, so it
// must be the most ordinary one there is.
func TestQUICALPNIsHTTP3(t *testing.T) {
	t.Parallel()
	if QUICALPN != "h3" {
		t.Fatalf("ALPN = %q; anything but h3 is unique and therefore visible", QUICALPN)
	}
}
