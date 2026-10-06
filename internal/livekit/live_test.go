//go:build live

package livekit

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
	"github.com/LywwKkA-aD/Gul/internal/session"
	lk "github.com/livekit/protocol/livekit"
	lklog "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

// This uses only the explicitly opted-in loopback broker and SFU. The audio
// source is a generated tone: no microphone, speaker or private server token.
func TestLocalSFUTwoNativeManagers(t *testing.T) {
	if os.Getenv("GUL_LIVEKIT_LIVE") != "1" {
		t.Skip("set GUL_LIVEKIT_LIVE=1 for the local stand")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	messages := make(chan session.RawMessage, 8)
	var treeMu sync.Mutex
	var tree domain.ChannelNode
	a := NewManager(logger, session.Callbacks{})
	b := NewManager(logger, session.Callbacks{OnMessage: func(message session.RawMessage) { messages <- message }, OnTree: func(next domain.ChannelNode) { treeMu.Lock(); tree = next; treeMu.Unlock() }})
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	suffix := time.Now().UnixNano() % 1_000_000
	a.Connect("http://127.0.0.1:8787", fmt.Sprintf("native-a-%d", suffix), "")
	b.Connect("http://127.0.0.1:8787", fmt.Sprintf("native-b-%d", suffix), "")
	liveWait(t, func() bool {
		return a.Status().State == domain.StateConnected && b.Status().State == domain.StateConnected
	}, func() string { return fmt.Sprintf("states a=%+v b=%+v", a.Status(), b.Status()) })
	// Subscribe signalling has a separate round trip after both peers join.
	time.Sleep(200 * time.Millisecond)
	liveVoice(t, a, b, 30)
	liveVoice(t, b, a, 30)
	liveScreenSound(t, a, b)
	text := "plain <text> & no markup"
	if err := a.SendMessage(a.Status().SelfChannel, text); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-messages:
		if got.HTML != html.EscapeString(text) {
			t.Fatalf("chat=%q", got.HTML)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("chat timeout")
	}
	select {
	case <-messages:
		t.Fatal("duplicate chat")
	case <-time.After(50 * time.Millisecond):
	}
	oldEpoch := b.Status().Epoch
	if err := b.Join(2); err != nil {
		t.Fatal(err)
	}
	liveWait(t, func() bool { return b.Status().State == domain.StateConnected && b.Status().SelfChannel == 2 }, func() string { return fmt.Sprint(b.Status()) })
	if _, err := b.ScreenGrant(context.Background(), oldEpoch, 0); err == nil {
		t.Fatal("old epoch grant accepted")
	}
	drainVoice(b)
	liveSend(t, a, 10)
	liveNoVoice(t, b, a.Status().SelfSession, 150*time.Millisecond)
	if err := a.Join(2); err != nil {
		t.Fatal(err)
	}
	liveWait(t, func() bool { return a.Status().State == domain.StateConnected && a.Status().SelfChannel == 2 }, func() string { return fmt.Sprint(a.Status()) })
	time.Sleep(200 * time.Millisecond)
	liveVoice(t, a, b, 20)
	a.SetSelfAudio(true, true)
	liveWait(t, func() bool { return a.SelfAudioSettled(true, true) }, func() string { return "mute not acknowledged" })
	liveWait(t, func() bool {
		treeMu.Lock()
		defer treeMu.Unlock()
		peer, ok := liveUser(tree, a.Status().SelfSession)
		return ok && peer.SelfMute && peer.SelfDeaf
	}, func() string { return "mute/deafen not reflected in remote roster" })
	drainVoice(b)
	liveSend(t, a, 5)
	liveNoVoice(t, b, a.Status().SelfSession, 150*time.Millisecond)
	a.SetSelfAudio(false, false)
	liveWait(t, func() bool { return a.SelfAudioSettled(false, false) }, func() string { return "unmute not acknowledged" })
	liveWait(t, func() bool {
		treeMu.Lock()
		defer treeMu.Unlock()
		peer, ok := liveUser(tree, a.Status().SelfSession)
		return ok && !peer.SelfMute && !peer.SelfDeaf
	}, func() string { return "unmute not reflected in remote roster" })
	liveVoice(t, a, b, 20)
	// Exercise the real manager's reconnect lifecycle while retaining the
	// broker session and recreating the actual WebRTC connection.
	before := a.Status().Epoch
	a.mu.Lock()
	current := a.media.(*sdkMedia)
	a.mu.Unlock()
	current.hooks.reconnect()
	liveWait(t, func() bool { return a.Status().State == domain.StateConnected && a.Status().Epoch > before }, func() string { return fmt.Sprint(a.Status()) })
	time.Sleep(200 * time.Millisecond)
	liveVoice(t, a, b, 20)
	if err := a.Join(0); err != nil {
		t.Fatal(err)
	}
	liveWait(t, func() bool { return a.Status().State == domain.StateConnected && a.Status().SelfChannel == 0 }, func() string { return fmt.Sprint(a.Status()) })
	grant, err := a.ScreenGrant(context.Background(), a.Status().Epoch, 0)
	if err != nil || grant.ChannelID != 0 || grant.Identity != "screen."+fmt.Sprint(a.Status().SelfSession) {
		t.Fatalf("screen grant metadata invalid: %v", err)
	}
	t.Log("two native managers: bidirectional audible Opus, chat, room isolation, mute/deafen, media reconnect, root room and screen epoch checks passed")
}

func liveUser(tree domain.ChannelNode, id uint32) (domain.UserInfo, bool) {
	for _, u := range tree.Users {
		if u.Session == id {
			return u, true
		}
	}
	for _, child := range tree.Children {
		if u, ok := liveUser(child, id); ok {
			return u, true
		}
	}
	return domain.UserInfo{}, false
}

func liveWait(t *testing.T, condition func() bool, diagnostic func() string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(diagnostic())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func drainVoice(m *Manager) {
	for {
		select {
		case <-m.VoicePackets():
		default:
			return
		}
	}
}
func liveSend(t *testing.T, m *Manager, count int) {
	liveTone(t, func(data []byte, final bool) error { return m.SendVoice(data, final) }, count)
}
func liveTone(t *testing.T, send func([]byte, bool) error, count int) {
	t.Helper()
	encoder, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	frame := make([]int16, 480)
	packet := make([]byte, opus.MaxEncodedBytes)
	for n := range count {
		for i := range frame {
			frame[i] = int16(10000 * math.Sin(2*math.Pi*440*float64(n*480+i)/48000))
		}
		data, err := encoder.Encode(frame, packet)
		if err != nil {
			t.Fatal(err)
		}
		if err := send(data, n == count-1); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func liveVoice(t *testing.T, from, to *Manager, count int) {
	t.Helper()
	owner := from.Status().SelfSession
	// The first RTP packet creates Pion's remote track and completes SFU
	// forwarding setup. Prime that path before measuring steady delivery.
	liveSend(t, from, 3)
	primed := false
	primeDeadline := time.After(3 * time.Second)
	for !primed {
		select {
		case p := <-to.VoicePackets():
			primed = p.Session == owner && len(p.Opus) > 0 && opusSamples(p.Opus) == 480
		case <-primeDeadline:
			t.Fatal("remote voice track did not become ready")
		}
	}
	time.Sleep(250 * time.Millisecond)
	drainVoice(to)
	liveSend(t, from, count)
	decoder, err := opus.NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	pcm := make([]int16, 2880)
	received, audible := 0, 0
	deadline := time.After(3 * time.Second)
	for received < count {
		select {
		case packet := <-to.VoicePackets():
			if len(packet.Opus) == 0 || packet.Session != owner {
				continue
			}
			n, err := decoder.Decode(packet.Opus, pcm)
			if err != nil {
				t.Fatal(err)
			}
			// LiveKit may prefill a newly subscribed stream with 20ms
			// silence. Only our forwarded 10ms tone frames count here.
			if n != opus.FrameSize {
				continue
			}
			received++
			for _, v := range pcm[:n] {
				if v > 500 || v < -500 {
					audible++
					break
				}
			}
		case <-deadline:
			t.Fatalf("voice received=%d/%d audible=%d stats=%+v", received, count, audible, from.VoiceStats())
		}
	}
	if audible < count-1 {
		t.Fatalf("decoded audio was silent: %d/%d", audible, count)
	}
}
func liveNoVoice(t *testing.T, m *Manager, owner uint32, duration time.Duration) {
	t.Helper()
	decoder, err := opus.NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	pcm := make([]int16, 2880)
	deadline := time.After(duration)
	for {
		select {
		case packet := <-m.VoicePackets():
			if len(packet.Opus) > 0 && packet.Session == owner {
				n, err := decoder.Decode(packet.Opus, pcm)
				if err != nil {
					t.Fatal(err)
				}
				peak := 0
				for _, v := range pcm[:max(0, n)] {
					peak = max(peak, int(v), -int(v))
				}
				// The SFU emits Opus silence after a mute signal. It is not
				// microphone transmission and must remain inaudible.
				if peak > 500 {
					t.Fatalf("audible audio escaped isolation/mute: peak=%d", peak)
				}
			}
		case <-deadline:
			return
		}
	}
}

func liveScreenSound(t *testing.T, owner, listener *Manager) {
	t.Helper()
	status := owner.Status()
	grant, err := owner.ScreenGrant(context.Background(), status.Epoch, status.SelfChannel)
	if err != nil {
		t.Fatal(err)
	}
	room := lksdk.NewRoom(lksdk.NewRoomCallback())
	room.SetLogger(lklog.GetDiscardLogger())
	if err := room.JoinWithContextAndToken(context.Background(), grant.URL, grant.Token, lksdk.WithAutoSubscribe(false), lksdk.WithDisableRegionDiscovery(), lksdk.WithDisableTURN(), lksdk.WithLogger(lklog.GetDiscardLogger())); err != nil {
		t.Fatal("screen companion could not join")
	}
	defer room.Disconnect()
	track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;stereo=0"})
	if err != nil {
		t.Fatal("screen track setup failed")
	}
	defer track.Close()
	if _, err := room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{Name: "generated screen sound", Source: lk.TrackSource_SCREEN_SHARE_AUDIO, DisableDTX: true}); err != nil {
		t.Fatal("screen track publish failed")
	}
	if err := waitBound(context.Background(), track.IsBound, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	drainVoice(owner)
	drainVoice(listener)
	clock := newTXClock(100, 10000, time.Now())
	liveTone(t, func(data []byte, final bool) error { return track.WriteRTP(clock.packet(data, final, time.Now()), nil) }, 20)
	decoder, err := opus.NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	pcm := make([]int16, 2880)
	count, audible := 0, 0
	deadline := time.After(3 * time.Second)
	for count < 20 {
		select {
		case p := <-listener.VoicePackets():
			if len(p.Opus) == 0 || p.Session != status.SelfSession|0x80000000 {
				continue
			}
			if p.Key != fmt.Sprintf("s:livekit:%d", status.SelfSession) {
				t.Fatal("screen sound lost owner or independent decoder mapping")
			}
			n, err := decoder.Decode(p.Opus, pcm)
			if err != nil {
				t.Fatal(err)
			}
			if n != opus.FrameSize {
				continue
			}
			count++
			for _, v := range pcm[:n] {
				if v > 500 || v < -500 {
					audible++
					break
				}
			}
		case <-deadline:
			t.Fatalf("screen sound received %d/20", count)
		}
	}
	if audible < 19 {
		t.Fatalf("screen sound decoded silence: %d/20", audible)
	}
	liveNoVoice(t, owner, status.SelfSession|0x80000000, 100*time.Millisecond)
}
