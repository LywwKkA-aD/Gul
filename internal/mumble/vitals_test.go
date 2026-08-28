package mumble

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
	"github.com/LywwKkA-aD/gumble/gumble"
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

// voiceCapture collects log records so a test can ask what a line contains.
type voiceCapture struct{ records []slog.Record }

func (h *voiceCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *voiceCapture) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *voiceCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *voiceCapture) WithGroup(string) slog.Handler      { return h }

// attrs flattens the named record's attributes, groups included, into
// "group.key" -> value.
func (h *voiceCapture) attrs(message string) map[string]string {
	out := make(map[string]string)
	for _, r := range h.records {
		if r.Message != message {
			continue
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Value.Kind() == slog.KindGroup {
				for _, inner := range a.Value.Group() {
					out[a.Key+"."+inner.Key] = inner.Value.String()
				}
				return true
			}
			if v, ok := a.Value.Any().(slog.LogValuer); ok {
				for _, inner := range v.LogValue().Group() {
					out[a.Key+"."+inner.Key] = inner.Value.String()
				}
				return true
			}
			out[a.Key] = a.Value.String()
			return true
		})
	}
	return out
}

// Every way a voice frame can be lost between the codec and the socket, named
// apart. A frame dropped because the sender was behind, one dropped because
// there was no session, and one the socket refused are three different faults
// and only the counts tell them apart.
func TestTheVoiceCountersNameEveryWayAFrameIsLost(t *testing.T) {
	t.Parallel()
	stats := VoiceStats{RXDrops: 1, TXDrops: 2, TXOffline: 3, TXErrors: 4}

	got := make(map[string]string)
	for _, attr := range stats.LogValue().Group() {
		got[attr.Key] = attr.Value.String()
	}
	for key, want := range map[string]string{
		"rx_drops": "1", "tx_drops": "2", "tx_offline": "3", "tx_errors": "4",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
}

// The counters have to reach the log without anyone asking.
//
// They existed for the whole of the last incident and no production line read
// them, so a report of "my voice stopped going out" could not be told apart
// from "my connection stopped" - which is the exact question that took three
// releases to answer.
func TestTheSessionPanelCarriesTheVoiceCounters(t *testing.T) {
	t.Parallel()
	capture := &voiceCapture{}
	manager, err := NewManager(t.TempDir(), slog.New(capture), Callbacks{})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(manager.Close)

	manager.logVitals(&Session{packets: newPacketConn(&discardConn{}), addr: "example.test"}, TransportWSS)

	got := capture.attrs("session vitals")
	for _, key := range []string{"voice.rx_drops", "voice.tx_drops", "voice.tx_offline", "voice.tx_errors"} {
		if _, ok := got[key]; !ok {
			t.Errorf("the session panel says nothing about %s", key)
		}
	}
	if _, ok := got["vitals.sent"]; !ok {
		t.Error("the session panel lost its connection counters")
	}
}

// The last line a lost session writes has to carry them too.
//
// The session panel runs every five seconds; a session that dies between two
// ticks would otherwise be described by a reading up to five seconds stale,
// and the counters that matter are exactly the ones that moved just before it
// died. This line is written even when the session had no panel of its own,
// because the voice counters live on the Manager and outlive it.
func TestTheLostConnectionLineCarriesTheVoiceCounters(t *testing.T) {
	t.Parallel()
	capture := &voiceCapture{}
	manager, err := NewManager(t.TempDir(), slog.New(capture), Callbacks{})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(manager.Close)
	manager.backoffFn = func(int) time.Duration { return time.Millisecond }
	manager.deriveFn = func([]byte) relayproto.Credential { return "v2.test-credential" }

	hooksCh := make(chan sessionHooks, 1)
	var attempts atomic.Int32
	manager.dialFn = func(_ DialConfig, hooks sessionHooks) (*Session, error) {
		if attempts.Add(1) == 1 {
			hooksCh <- hooks
			return &Session{}, nil
		}
		return nil, errors.New("no second attempt wanted")
	}

	manager.Connect("wss://murmur.example.test/mumble", "gul", "secret")
	hooks := <-hooksCh
	hooks.disconnect(&gumble.DisconnectEvent{Type: gumble.DisconnectError, String: "reset"})

	deadline := time.Now().Add(3 * time.Second)
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	manager.Disconnect()

	got := capture.attrs("connection lost")
	if len(got) == 0 {
		t.Fatal("no connection lost line was written; this test proves nothing")
	}
	for _, key := range []string{"voice.rx_drops", "voice.tx_drops", "voice.tx_offline", "voice.tx_errors"} {
		if _, ok := got[key]; !ok {
			t.Errorf("the lost connection line says nothing about %s", key)
		}
	}
}
