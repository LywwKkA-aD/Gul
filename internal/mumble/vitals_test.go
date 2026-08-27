package mumble

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The panel has to survive the stream arriving in pieces, because that is the
// only way it ever arrives: one Read can carry half a header, and the packet
// whose type matters most - serversync, reject - is as likely to straddle a
// boundary as any other.
func TestInboundFramerCountsPacketsWhateverTheChunks(t *testing.T) {
	t.Parallel()
	stream := concat(
		mumblePacket(0, []byte("version")),
		mumblePacket(5, nil), // serversync, empty payload
		mumblePacket(1, make([]byte, 300)),
		mumblePacket(1, make([]byte, 300)),
		mumblePacket(3, []byte("ping")),
	)
	want := "version=1 udptunnel=2 ping=1 serversync=1"

	for _, chunk := range []int{1, 3, packetHeaderBytes, 7, 64, len(stream)} {
		var framer inboundFramer
		var counts tally
		for i := 0; i < len(stream); i += chunk {
			framer.consume(stream[i:min(i+chunk, len(stream))], &counts)
		}
		if got := counts.String(); got != want {
			t.Fatalf("chunks of %d: tally = %q, want %q", chunk, got, want)
		}
	}
}

// A payload longer than the relay would ever carry means the framer is not
// where it thinks it is. Counting is a diagnostic: it has to go quiet rather
// than report numbers nobody can trust.
func TestInboundFramerStopsRatherThanGuessing(t *testing.T) {
	t.Parallel()
	var framer inboundFramer
	var counts tally

	framer.consume(concat(
		mumblePacket(0, []byte("version")),
		[]byte{0, 7, 0xFF, 0xFF, 0xFF, 0xFF},
	), &counts)
	framer.consume(concat(mumblePacket(5, nil), mumblePacket(3, nil)), &counts)

	if got := counts.String(); got != "version=1" {
		t.Fatalf("tally = %q, want only the packet read before the stream stopped making sense", got)
	}
}

// The three ways a session can go quiet, told apart. This is the whole point
// of the panel: from a journal alone they look identical, and the fix for each
// is different.
func TestVitalsSeparateAStuckWriteFromASilentOne(t *testing.T) {
	t.Parallel()

	t.Run("a write of ours that has not returned", func(t *testing.T) {
		t.Parallel()
		inner := &stallingConn{}
		conn := newPacketConn(inner)
		conn.writeTimeout = 400 * time.Millisecond
		conn.lastRead.Store(time.Now().UnixNano())

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = conn.Write(mumblePacket(1, []byte("voice")))
		}()

		var blocked time.Duration
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			if blocked = conn.Vitals().Blocked; blocked > 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if blocked <= 0 {
			t.Fatal("a write that is still in flight was not visible on the panel")
		}
		<-done

		v := conn.Vitals()
		if v.Blocked != 0 {
			t.Fatalf("blocked = %v after the write returned, want none in flight", v.Blocked)
		}
		if v.Slowest < conn.writeTimeout {
			t.Fatalf("slowest = %v, want at least the write timeout %v", v.Slowest, conn.writeTimeout)
		}
		if !strings.Contains(v.Out, "udptunnel=1") {
			t.Fatalf("out = %q, want the attempted voice packet counted", v.Out)
		}
		if !v.Stalled {
			t.Fatal("the stall was not on the panel")
		}
	})

	t.Run("nothing attempted at all", func(t *testing.T) {
		t.Parallel()
		conn := newPacketConn(&readOnceConn{payload: mumblePacket(5, nil)})

		buf := make([]byte, 64)
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("read: %v", err)
		}

		v := conn.Vitals()
		if v.Out != "" {
			t.Fatalf("out = %q, want nothing sent", v.Out)
		}
		if v.In != "serversync=1" {
			t.Fatalf("in = %q, want the packet that arrived", v.In)
		}
		if v.Blocked != 0 {
			t.Fatalf("blocked = %v, want no write in flight", v.Blocked)
		}
		if v.Received == 0 {
			t.Fatal("received bytes were not counted")
		}
	})

	t.Run("writes that complete and cross", func(t *testing.T) {
		t.Parallel()
		conn := newPacketConn(&discardConn{})

		packet := mumblePacket(3, []byte("ping"))
		if _, err := conn.Write(packet); err != nil {
			t.Fatalf("write: %v", err)
		}

		v := conn.Vitals()
		if v.Sent != int64(len(packet)) {
			t.Fatalf("sent = %d, want %d", v.Sent, len(packet))
		}
		if v.Out != "ping=1" {
			t.Fatalf("out = %q, want the ping counted", v.Out)
		}
		if v.Stalled {
			t.Fatal("a healthy write was reported as a stall")
		}
	})
}

// Silence is measured from the connection, not from the epoch: a session where
// nothing ever arrived has to report a growing silence rather than a number
// counted from 1970.
func TestVitalsMeasureSilenceFromTheConnection(t *testing.T) {
	t.Parallel()
	conn := newPacketConn(&discardConn{})
	conn.created = time.Now().Add(-3 * time.Second)

	if silent := conn.Vitals().Silent; silent < 3*time.Second || silent > time.Minute {
		t.Fatalf("silent = %v, want about three seconds", silent)
	}

	conn.lastRead.Store(time.Now().UnixNano())
	if silent := conn.Vitals().Silent; silent > time.Second {
		t.Fatalf("silent = %v after a read, want it measured from the read", silent)
	}
}

// The panel goes into diagnostics archives that users hand to other people. A
// socket error is exactly where an address turns up, so it is the one field on
// the panel that has to be rewritten before it is logged.
func TestVitalsCannotNameTheServer(t *testing.T) {
	t.Parallel()
	v := Vitals{
		Out: "ping=3",
		Err: errors.New("write tcp 192.168.0.106:60832->203.0.113.9:443: " +
			"connection to murmur.example.test was aborted"),
	}

	rendered := v.redact("wss://murmur.example.test").LogValue().String()

	for _, secret := range []string{"192.168.0.106", "203.0.113.9", "murmur.example.test"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("the panel carried %q: %s", secret, rendered)
		}
	}
	// The diagnosis itself has to survive, or the redaction has removed the
	// reason the archive was collected.
	if !strings.Contains(rendered, "aborted") || !strings.Contains(rendered, "ping=3") {
		t.Fatalf("redaction took the diagnosis with it: %s", rendered)
	}
}

// concat joins packets into one stream.
func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
