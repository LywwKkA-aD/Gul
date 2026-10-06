//go:build live

package livekit

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/session"
)

// This peer is an opt-in UI test fixture, never a background microphone or
// recording process. It uses only aggregate counters and discards decoded PCM.
// GUL_LIVEKIT_PEER=1 enables it; DURATION defaults to 60s, CHANNEL to 1,
// and TONE=1 additionally publishes a generated 440Hz tone every four seconds.
func TestLocalSFUInteractivePeer(t *testing.T) {
	if os.Getenv("GUL_LIVEKIT_PEER") != "1" {
		t.Skip("set GUL_LIVEKIT_PEER=1 to start the local UI test peer")
	}
	duration := 60 * time.Second
	if value := os.Getenv("GUL_LIVEKIT_PEER_DURATION"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Second || parsed > 30*time.Minute {
			t.Fatal("peer duration must be between 1s and 30m")
		}
		duration = parsed
	}
	channel := 1
	if value := os.Getenv("GUL_LIVEKIT_PEER_CHANNEL"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 3 {
			t.Fatal("peer channel must be 0, 1, 2 or 3")
		}
		channel = parsed
	}
	tone := os.Getenv("GUL_LIVEKIT_PEER_TONE") == "1"
	var messages atomic.Uint64
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), session.Callbacks{
		OnMessage: func(session.RawMessage) { messages.Add(1) },
	})
	defer m.Close()
	m.Connect("http://127.0.0.1:8787", "Local test peer", "")
	liveWait(t, func() bool { return m.Status().State == domain.StateConnected }, func() string { return "local test peer did not connect" })
	if err := m.Join(uint32(channel)); err != nil {
		t.Fatal(err)
	}
	liveWait(t, func() bool {
		return m.Status().State == domain.StateConnected && m.Status().SelfChannel == uint32(channel)
	}, func() string { return "local test peer did not join requested channel" })
	t.Logf("local peer ready: channel=%d duration=%s generated_tone=%t", channel, duration, tone)
	announce := func() {
		status := m.Status()
		if status.State == domain.StateConnected {
			if err := m.SendMessage(status.SelfChannel, "Local test peer: LiveKit connection is working."); err != nil {
				t.Fatal(err)
			}
		}
	}
	announce()
	end := time.NewTimer(duration)
	defer end.Stop()
	report := time.NewTicker(10 * time.Second)
	defer report.Stop()
	tones := time.NewTicker(4 * time.Second)
	defer tones.Stop()
	decoders := make(map[uint32]*opus.Decoder)
	closeDecoders := func() {
		for id, decoder := range decoders {
			decoder.Close()
			delete(decoders, id)
		}
	}
	defer closeDecoders()
	pcm := make([]int16, opus.MaxFrameSize)
	packets, audible := uint64(0), uint64(0)
	summary := func() {
		t.Logf("local peer counters: opus_packets=%d audible_packets=%d chat_messages=%d", packets, audible, messages.Load())
	}
	for {
		select {
		case <-end.C:
			summary()
			return
		case <-report.C:
			summary()
			announce()
		case <-tones.C:
			if tone {
				liveSend(t, m, 20)
			}
		case packet := <-m.VoicePackets():
			if packet.Reset {
				closeDecoders()
				continue
			}
			decoder := decoders[packet.Session]
			if packet.LostFrames > 0 && decoder != nil {
				for range min(packet.LostFrames, 12) {
					_, _ = decoder.Decode(nil, pcm[:480])
				}
				clear(pcm)
				continue
			}
			if len(packet.Opus) == 0 {
				continue
			}
			if decoder == nil {
				var err error
				decoder, err = opus.NewDecoder()
				if err != nil {
					t.Fatal(err)
				}
				decoders[packet.Session] = decoder
			}
			n, err := decoder.Decode(packet.Opus, pcm)
			if err != nil {
				t.Fatal(err)
			}
			packets++
			for _, v := range pcm[:n] {
				if v > 500 || v < -500 {
					audible++
					break
				}
			}
			clear(pcm)
		}
	}
}
