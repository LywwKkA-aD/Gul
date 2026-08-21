package mumble

import (
	"errors"
	"testing"
	"time"

	"github.com/LywwKkA-aD/gumble/gumble"
)

func TestVoiceStubCodecRefusesToWork(t *testing.T) {
	codec := voiceStubCodec{}
	if got := codec.ID(); got != voiceCodecOpus {
		t.Fatalf("codec ID = %d, want %d", got, voiceCodecOpus)
	}

	encoder := codec.NewEncoder()
	if encoder == nil {
		t.Fatal("NewEncoder returned nil; gumble calls it on CodecVersion and would store nil")
	}
	if got := encoder.ID(); got != voiceCodecOpus {
		t.Fatalf("encoder ID = %d, want %d", got, voiceCodecOpus)
	}
	if _, err := encoder.Encode([]int16{0, 1, 2}, 3, 40); !errors.Is(err, errStubCodec) {
		t.Fatalf("Encode error = %v, want %v", err, errStubCodec)
	}
	encoder.Reset()

	decoder := codec.NewDecoder()
	if decoder == nil {
		t.Fatal("NewDecoder returned nil")
	}
	if got := decoder.ID(); got != voiceCodecOpus {
		t.Fatalf("decoder ID = %d, want %d", got, voiceCodecOpus)
	}
	if _, err := decoder.Decode([]byte{1, 2, 3}, 480); !errors.Is(err, errStubCodec) {
		t.Fatalf("Decode error = %v, want %v", err, errStubCodec)
	}
	decoder.Reset()
}

func TestCodecRegistrarInstallsStubOnce(t *testing.T) {
	var ids []int
	var codecs []gumble.AudioCodec

	registrar := codecRegistrar{register: func(id int, codec gumble.AudioCodec) {
		ids = append(ids, id)
		codecs = append(codecs, codec)
	}}
	for range 3 {
		registrar.ensure()
	}

	if len(ids) != 1 {
		t.Fatalf("registered %d times, want exactly 1", len(ids))
	}
	if ids[0] != voiceCodecOpus {
		t.Fatalf("registered under id %d, want %d", ids[0], voiceCodecOpus)
	}
	if _, ok := codecs[0].(voiceStubCodec); !ok {
		t.Fatalf("registered codec is %T, want voiceStubCodec", codecs[0])
	}
}

func TestRegisterVoiceCodecIsRepeatable(t *testing.T) {
	// The real registry is process-wide, so this only checks that the guarded
	// entry point stays callable from every dial attempt.
	registerVoiceCodec()
	registerVoiceCodec()
}

func TestDropBufferKeepsOrderWhileItFits(t *testing.T) {
	buffer := newDropBuffer[int](4)
	for i := range 4 {
		buffer.push(i)
	}

	for want := range 4 {
		select {
		case got := <-buffer.out():
			if got != want {
				t.Fatalf("got %d, want %d", got, want)
			}
		default:
			t.Fatalf("buffer ran dry at %d", want)
		}
	}
	if got := buffer.dropped(); got != 0 {
		t.Fatalf("drops = %d, want 0", got)
	}
}

func TestDropBufferEvictsOldestAndCounts(t *testing.T) {
	const (
		capacity = 4
		pushed   = 10
	)

	buffer := newDropBuffer[int](capacity)
	for i := range pushed {
		buffer.push(i)
	}

	if got, want := buffer.dropped(), uint64(pushed-capacity); got != want {
		t.Fatalf("drops = %d, want %d", got, want)
	}
	for want := pushed - capacity; want < pushed; want++ {
		select {
		case got := <-buffer.out():
			if got != want {
				t.Fatalf("got %d, want %d (the newest items must survive)", got, want)
			}
		default:
			t.Fatalf("buffer ran dry at %d", want)
		}
	}
}

func TestDropBufferPushNeverBlocks(t *testing.T) {
	buffer := newDropBuffer[int](1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := range 10_000 {
			buffer.push(i)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("push blocked on a full buffer")
	}
}

// TestVoiceListenerNeverBlocksReadLoop is the contract that keeps the
// connection alive: gumble writes stream packets from its single read loop over
// an unbuffered channel, so a slow RX consumer must cost frames, never time.
func TestVoiceListenerNeverBlocksReadLoop(t *testing.T) {
	const frames = 1000

	buffer := newDropBuffer[VoicePacket](voiceRXBuffer)
	listener := newVoiceListener(buffer)
	t.Cleanup(listener.stop)

	stream := make(chan *gumble.AudioPacket)
	listener.OnAudioStream(&gumble.AudioStreamEvent{C: stream})

	// Nothing ever reads VoicePackets here: this is the "consumer is wedged"
	// case the drop-oldest buffer exists for.
	start := time.Now()
	for i := range frames {
		stream <- &gumble.AudioPacket{
			Sequence: int64(i),
			OpusData: []byte{byte(i)},
			Final:    i == frames-1,
		}
	}
	elapsed := time.Since(start)
	close(stream)

	if elapsed > 2*time.Second {
		t.Fatalf("read loop spent %s pushing %d frames; it must not be throttled by the consumer", elapsed, frames)
	}

	listener.stop()

	if got, want := buffer.dropped(), uint64(frames-voiceRXBuffer); got != want {
		t.Fatalf("drops = %d, want %d", got, want)
	}
	// What survives is the tail, in order.
	for want := frames - voiceRXBuffer; want < frames; want++ {
		select {
		case packet := <-buffer.out():
			if packet.Sequence != int64(want) {
				t.Fatalf("sequence = %d, want %d", packet.Sequence, want)
			}
		default:
			t.Fatalf("buffer ran dry at %d", want)
		}
	}
}

func TestVoiceListenerCopiesSenderIdentity(t *testing.T) {
	buffer := newDropBuffer[VoicePacket](4)
	listener := newVoiceListener(buffer)
	t.Cleanup(listener.stop)

	stream := make(chan *gumble.AudioPacket)
	user := &gumble.User{Session: 42, Hash: "abc123"}
	listener.OnAudioStream(&gumble.AudioStreamEvent{User: user, C: stream})

	stream <- &gumble.AudioPacket{Sequence: 7, OpusData: []byte{9}, Final: true}
	close(stream)

	select {
	case packet := <-buffer.out():
		if packet.Session != 42 || packet.Hash != "abc123" {
			t.Fatalf("sender = (%d, %q), want (42, \"abc123\")", packet.Session, packet.Hash)
		}
		if packet.Sequence != 7 || !packet.Final || len(packet.Opus) != 1 || packet.Opus[0] != 9 {
			t.Fatalf("packet = %+v, want sequence 7, final, opus [9]", packet)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the forwarded packet")
	}
}

// TestVoiceListenerStopReleasesParkedReadLoop covers our own disconnect: gumble
// closes stream channels when the *sender* leaves, never when we do, so the
// pumps need an exit that also unsticks a read loop parked mid-send.
func TestVoiceListenerStopReleasesParkedReadLoop(t *testing.T) {
	buffer := newDropBuffer[VoicePacket](1)
	listener := newVoiceListener(buffer)

	stream := make(chan *gumble.AudioPacket)
	listener.OnAudioStream(&gumble.AudioStreamEvent{C: stream})

	// A read loop that keeps handing packets over while the session is being
	// torn down. The buffer holds one, so every further handoff needs the pump
	// to still be receiving.
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for range 5 {
			stream <- &gumble.AudioPacket{OpusData: []byte{1}}
		}
	}()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		listener.stop()
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return; a pump is stuck")
	}
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the simulated read loop stayed parked after stop")
	}
}

func TestVoiceListenerStopIsIdempotent(t *testing.T) {
	listener := newVoiceListener(newDropBuffer[VoicePacket](1))
	listener.stop()
	listener.stop()

	// A stream arriving after stop must still be drained, or gumble's read loop
	// would park on the handoff.
	stream := make(chan *gumble.AudioPacket)
	listener.OnAudioStream(&gumble.AudioStreamEvent{C: stream})

	done := make(chan struct{})
	go func() {
		defer close(done)
		stream <- &gumble.AudioPacket{OpusData: []byte{1}}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stream opened after stop was never drained")
	}
	close(stream)
}

func TestVoiceSequenceCountsFramesAndResetsAfterFinal(t *testing.T) {
	var seq voiceSequence

	for want := range int64(5) {
		if got := seq.step(false); got != want {
			t.Fatalf("step %d = %d, want %d", want, got, want)
		}
	}
	// The terminator rides on the last frame of the transmission, so it takes
	// the next number rather than a special one.
	if got := seq.step(true); got != 5 {
		t.Fatalf("final step = %d, want 5", got)
	}
	// The next transmission is counted from scratch.
	if got := seq.step(false); got != 0 {
		t.Fatalf("step after final = %d, want 0", got)
	}
	if got := seq.step(false); got != 1 {
		t.Fatalf("second step after final = %d, want 1", got)
	}

	seq.reset()
	if got := seq.step(false); got != 0 {
		t.Fatalf("step after reset = %d, want 0", got)
	}
}

func TestVoiceSequenceWrapsLikeGumble(t *testing.T) {
	seq := voiceSequence{next: voiceSequenceWrap - 1}

	if got := seq.step(false); got != voiceSequenceWrap-1 {
		t.Fatalf("step = %d, want %d", got, voiceSequenceWrap-1)
	}
	if got := seq.step(false); got != 0 {
		t.Fatalf("step after wrap = %d, want 0", got)
	}
}

func TestVoiceProtoMode(t *testing.T) {
	if voiceProtoMode(nil) {
		t.Fatal("a nil client must not claim protobuf audio")
	}
	if voiceProtoMode(&gumble.Client{}) {
		t.Fatal("a client without a version must not claim protobuf audio")
	}

	legacy := &gumble.Client{Version: &gumble.Version{Version: uint64(1<<48 | 4<<32)}}
	if voiceProtoMode(legacy) {
		t.Fatal("a 1.4 server must use the legacy framing")
	}
	modern := &gumble.Client{Version: &gumble.Version{Version: uint64(1<<48 | 5<<32)}}
	if !voiceProtoMode(modern) {
		t.Fatal("a 1.5 server must use the protobuf framing")
	}
}

func TestManagerSendVoiceRejectsEmptyNonFinalFrame(t *testing.T) {
	m := newTestManager(t, Callbacks{})

	if err := m.SendVoice(nil, false); !errors.Is(err, ErrEmptyVoiceFrame) {
		t.Fatalf("SendVoice(nil, false) = %v, want %v", err, ErrEmptyVoiceFrame)
	}
	if err := m.SendVoice(nil, true); err != nil {
		t.Fatalf("a bare terminator is legal on the wire, got %v", err)
	}
}

func TestManagerSendVoiceOfflineDropsSilently(t *testing.T) {
	m := newTestManager(t, Callbacks{})

	const frames = 32
	for i := range frames {
		if err := m.SendVoice([]byte{byte(i)}, false); err != nil {
			t.Fatalf("SendVoice with no session must not be an error, got %v", err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		stats := m.VoiceStats()
		if stats.TXOffline+stats.TXDrops >= frames {
			if stats.TXErrors != 0 {
				t.Fatalf("TXErrors = %d, want 0 without a connection", stats.TXErrors)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d frames accounted for: %+v", stats.TXOffline+stats.TXDrops, frames, stats)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestManagerVoicePacketsSurviveReconnect pins the lifecycle contract: the
// audio pipeline grabs the channel once and keeps it while listeners are
// swapped underneath on every reconnect.
func TestManagerVoicePacketsSurviveReconnect(t *testing.T) {
	m := newTestManager(t, Callbacks{})
	packets := m.VoicePackets()

	for session := range uint32(3) {
		listener := m.voice.newListener()

		stream := make(chan *gumble.AudioPacket)
		listener.OnAudioStream(&gumble.AudioStreamEvent{
			User: &gumble.User{Session: session},
			C:    stream,
		})
		stream <- &gumble.AudioPacket{Sequence: int64(session), OpusData: []byte{byte(session)}}
		close(stream)

		select {
		case packet := <-packets:
			if packet.Session != session {
				t.Fatalf("session = %d, want %d", packet.Session, session)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no packet after listener swap %d", session)
		}
	}

	if got := m.VoiceDrops(); got != 0 {
		t.Fatalf("drops = %d, want 0", got)
	}
}

func TestManagerVoiceCloseIsIdempotent(t *testing.T) {
	m := newTestManager(t, Callbacks{})
	m.Close()
	m.Close()
}
