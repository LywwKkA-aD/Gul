package livekit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
	"github.com/pion/rtp"
)

type blockedMedia struct {
	fakeMedia
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockedMedia) write(p *rtp.Packet) error {
	f.once.Do(func() { close(f.started) })
	<-f.release
	return f.fakeMedia.write(p)
}

func TestVoiceBackpressureIsBoundedAndMuteDiscardsPendingFrames(t *testing.T) {
	m, _, _ := testManager(t)
	f := &blockedMedia{started: make(chan struct{}), release: make(chan struct{})}
	m.dial = func(context.Context, api.Grant, mediaHooks) (mediaConnection, error) { return f, nil }
	m.Connect("http://127.0.0.1:8787", "alice", "")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	if err := m.SendVoice([]byte{0xf0, 1}, false); err != nil {
		t.Fatal(err)
	}
	<-f.started
	for range 40 {
		if err := m.SendVoice([]byte{0xf0, 2}, false); err != nil {
			t.Fatal(err)
		}
	}
	if m.VoiceStats().TXDrops == 0 {
		t.Fatal("unbounded sender backlog")
	}
	m.SetSelfAudio(true, false)
	close(f.release)
	if err := m.SendVoice([]byte{0xf0, 3}, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return m.VoiceStats().TXOffline > 0 })
	time.Sleep(10 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) != 1 {
		t.Fatalf("pending voice escaped mute: %d packets", len(f.packets))
	}
}

func TestVoiceRejectsObsoleteEpochAndResetsDecoderQueue(t *testing.T) {
	m, _, f := testManager(t)
	if m.SendVoice(nil, false) == nil {
		t.Fatal("empty non-final accepted")
	}
	if err := m.SendVoice(nil, true); err != nil {
		t.Fatal(err)
	}
	m.Connect("http://127.0.0.1:8787", "alice", "")
	waitFor(t, func() bool { return m.Status().State == domain.StateConnected })
	m.voice.tx <- outgoing{epoch: m.Status().Epoch - 1, packet: packet(1, 1)}
	waitFor(t, func() bool { return m.VoiceStats().TXOffline == 1 })
	for i := range 300 {
		m.voice.receive(session.VoicePacket{Session: 1, Sequence: int64(i), Opus: []byte{1}})
	}
	if m.VoiceDrops() == 0 {
		t.Fatal("unbounded receive backlog")
	}
	m.Disconnect()
	p := <-m.VoicePackets()
	if !p.Reset || len(p.Opus) > 0 {
		t.Fatal("channel switch did not flush native decoder state")
	}
	select {
	case p := <-m.VoicePackets():
		t.Fatalf("old voice survived reset: %+v", p)
	default:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) != 0 {
		t.Fatal("obsolete epoch transmitted")
	}
}

func TestConnectValidationAndCloseStaySafe(t *testing.T) {
	m, _, _ := testManager(t)
	m.Connect("https://example.com", "alice", "private")
	if m.Status().Error != ErrLocalOnly.Error() {
		t.Fatal("remote endpoint accepted")
	}
	m.Connect("http://127.0.0.1:8787", "", "")
	if m.Status().Error != ErrAuthentication.Error() {
		t.Fatal("empty identity accepted")
	}
	if !errors.Is(m.Join(4), ErrStaleSession) {
		t.Fatal("invalid room accepted")
	}
	if m.SendMessage(0, "") == nil {
		t.Fatal("empty chat accepted")
	}
	if !errors.Is(m.SendMessage(0, "hello"), ErrNotConnected) {
		t.Fatal("offline chat accepted")
	}
	m.Close()
	m.Close()
	m.Connect("http://127.0.0.1:8787", "alice", "")
	if m.Status().State != domain.StateDisconnected {
		t.Fatal("closed manager reconnected")
	}
}
