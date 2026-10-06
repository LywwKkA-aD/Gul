//go:build live

package mumble

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/dsp/opus"
)

// TestPublicHysteriaChatAndVoice is opt-in: two fresh Gul identities exchange
// text and a second of audible Opus in each direction through the deployed
// Hysteria endpoint. It uses the normal client, TLS verification and Manager.
// Provide GUL_HYSTERIA_LIVE_ADDRESS and GUL_HYSTERIA_LIVE_PASSWORD_FILE.
func TestPublicHysteriaChatAndVoice(t *testing.T) {
	address := os.Getenv("GUL_HYSTERIA_LIVE_ADDRESS")
	passwordFile := os.Getenv("GUL_HYSTERIA_LIVE_PASSWORD_FILE")
	if address == "" || passwordFile == "" {
		t.Skip("GUL_HYSTERIA_LIVE_ADDRESS and GUL_HYSTERIA_LIVE_PASSWORD_FILE are required")
	}
	publicProxyChatAndVoice(t, address, passwordFile, endpointHysteria, TransportHysteria)
}

func TestPublicRealityChatAndVoice(t *testing.T) {
	address, passwordFile := publicRealityProfile(t)
	publicProxyChatAndVoice(t, address, passwordFile, endpointReality, TransportReality)
}

func publicRealityProfile(t *testing.T) (string, string) {
	t.Helper()
	addressFile := os.Getenv("GUL_REALITY_LIVE_ADDRESS_FILE")
	passwordFile := os.Getenv("GUL_REALITY_LIVE_PASSWORD_FILE")
	if addressFile == "" || passwordFile == "" {
		t.Skip("GUL_REALITY_LIVE_ADDRESS_FILE and GUL_REALITY_LIVE_PASSWORD_FILE are required")
	}
	data, err := os.ReadFile(addressFile)
	if err != nil {
		t.Fatal("could not read the private REALITY profile")
	}
	return strings.TrimSpace(string(data)), passwordFile
}

func publicProxyChatAndVoice(t *testing.T, address, passwordFile string, kind endpointKind, transport Transport) {
	t.Helper()
	ep, err := parseEndpoint(address)
	if err != nil || ep.kind != kind {
		t.Fatal("the public test requires a valid endpoint of the requested transport")
	}
	password := readLivePassword(t, passwordFile)
	defer clear(password)

	a := newPublicHysteriaClient(t)
	b := newPublicHysteriaClient(t)
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	nameA, nameB := "gul-check-a-"+suffix, "gul-check-b-"+suffix
	// An old saved direct hint must never create an alternate network path.
	a.mgr.PreferTransport(ep.address, "direct")
	b.mgr.PreferTransport(ep.address, "wss")
	a.mgr.Connect(ep.address, nameA, string(password))
	b.mgr.Connect(ep.address, nameB, string(password))
	waitState(t, a.liveClient, domain.StateConnected)
	waitState(t, b.liveClient, domain.StateConnected)

	root := waitRoot(t, a.liveClient)
	for _, client := range []*publicHysteriaClient{a, b} {
		if err := client.mgr.Join(root); err != nil {
			t.Fatal("a test client could not join the shared channel")
		}
		waitSelfInChannel(t, client.liveClient, root)
	}
	peerA := waitPublicPeer(t, b, nameA)
	peerB := waitPublicPeer(t, a, nameB)
	if peerA.Hash == "" || peerB.Hash == "" || peerA.Hash == peerB.Hash {
		t.Fatal("the server did not report two distinct client certificate identities")
	}

	publicHysteriaChat(t, a, b, nameA, root, "Gul transport check A to B "+suffix)
	publicHysteriaChat(t, b, a, nameB, root, "Gul transport check B to A "+suffix)
	publicHysteriaVoice(t, a, b, peerA.Session, "A to B")
	publicHysteriaVoice(t, b, a, peerB.Session, "B to A")

	for _, client := range []*publicHysteriaClient{a, b} {
		select {
		case proven := <-client.proven:
			if proven != transport {
				t.Fatal("a test client used an unexpected transport")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the transport did not prove a Mumble round trip")
		}
		if client.unstable.Load() || client.state() != domain.StateConnected {
			t.Fatal("a client disconnected or reconnected during the public test")
		}
		stats := client.mgr.VoiceStats()
		if stats.TXErrors != 0 || stats.TXOffline != 0 || stats.TXDrops != 0 {
			t.Fatalf("voice sender failed: errors=%d offline=%d dropped=%d",
				stats.TXErrors, stats.TXOffline, stats.TXDrops)
		}
	}
	t.Log("two independent clients exchanged chat and decoded audible Opus in both directions through the requested transport")
}

type publicHysteriaClient struct {
	*liveClient
	proven   chan Transport
	unstable atomic.Bool
}

func newPublicHysteriaClient(t *testing.T) *publicHysteriaClient {
	t.Helper()
	c := &publicHysteriaClient{liveClient: &liveClient{}, proven: make(chan Transport, 4)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	connected := false // Only OnStatus accesses this, under c.mu.
	manager, err := NewManager(t.TempDir(), log, Callbacks{
		OnStatus: func(status domain.ConnectionStatus) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if connected && status.State != domain.StateConnected {
				c.unstable.Store(true)
			}
			if status.State == domain.StateConnected {
				connected = true
			}
			c.st = status
		},
		OnTree: func(tree domain.ChannelNode) {
			c.mu.Lock()
			c.tr = &tree
			c.mu.Unlock()
		},
		OnMessage: func(message RawMessage) {
			c.mu.Lock()
			c.msg = append(c.msg, message)
			c.mu.Unlock()
		},
		OnTransport: func(_ string, transport string) {
			select {
			case c.proven <- Transport(transport):
			default:
			}
		},
	})
	if err != nil {
		t.Fatal("could not create the isolated public-test client")
	}
	c.mgr = manager
	// Observe the normal ping gate within this short test. This only changes
	// when its existing round-trip evidence is inspected.
	manager.roundTripGrace = 5 * time.Second
	t.Cleanup(manager.Close)
	return c
}

func waitPublicPeer(t *testing.T, observer *publicHysteriaClient, name string) domain.UserInfo {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		if tree := observer.tree(); tree != nil {
			if peer, ok := findUser(*tree, name); ok {
				return peer
			}
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("the observer did not receive the other test client's identity")
			return domain.UserInfo{}
		}
	}
}

func publicHysteriaChat(t *testing.T, from, to *publicHysteriaClient, sender string, channel uint32, probe string) {
	t.Helper()
	if err := from.mgr.SendMessage(channel, probe); err != nil {
		t.Fatal("the public chat message could not be sent")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		for _, message := range to.messages() {
			if message.HTML == probe && message.Sender == sender && message.ChannelID == channel {
				return
			}
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("the public chat message did not reach the other client")
		}
	}
}

func publicHysteriaVoice(t *testing.T, from, to *publicHysteriaClient, sender uint32, direction string) {
	t.Helper()
	encoder, err := opus.NewEncoder(40000)
	if err != nil {
		t.Fatalf("Opus encoder: %v", err)
	}
	defer encoder.Close()
	decoder, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("Opus decoder: %v", err)
	}
	defer decoder.Close()

	const frames = 100
	frame := make([]int16, opus.FrameSize)
	pace := time.NewTicker(10 * time.Millisecond)
	defer pace.Stop()
	for i := range frames {
		for n := range frame {
			seconds := float64(i*opus.FrameSize+n) / opus.SampleRate
			frame[n] = int16(0.3 * 32767 * math.Sin(2*math.Pi*440*seconds))
		}
		data, err := encoder.Encode(frame, nil)
		if err != nil {
			t.Fatalf("Opus encode: %v", err)
		}
		if err := from.mgr.SendVoice(data, false); err != nil {
			t.Fatal("could not queue the public voice frame")
		}
		<-pace.C
	}
	clear(frame)
	final, err := encoder.Encode(frame, nil)
	if err != nil {
		t.Fatalf("Opus terminator encode: %v", err)
	}
	if err := from.mgr.SendVoice(final, true); err != nil {
		t.Fatal("could not queue the voice terminator")
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	pcm := make([]int16, opus.MaxFrameSize)
	received, audible := 0, 0
	lastSequence := int64(-1)
	for {
		select {
		case packet := <-to.mgr.VoicePackets():
			if packet.Session != sender {
				continue
			}
			if packet.Sequence <= lastSequence {
				t.Fatalf("%s: voice sequence was repeated or reordered", direction)
			}
			lastSequence = packet.Sequence
			if packet.Final {
				if received < frames*9/10 || received > frames {
					t.Fatalf("%s: received %d of %d voice frames", direction, received, frames)
				}
				if audible < received/2 {
					t.Fatalf("%s: only %d of %d frames decoded to audible PCM", direction, audible, received)
				}
				t.Logf("%s: %d voice frames, %d audible, increasing sequences and terminator received",
					direction, received, audible)
				return
			}
			received++
			n, err := decoder.Decode(packet.Opus, pcm)
			if err != nil || n == 0 {
				t.Fatalf("%s: received voice could not be decoded as Opus", direction)
			}
			var energy float64
			for _, sample := range pcm[:n] {
				energy += float64(sample) * float64(sample)
			}
			if math.Sqrt(energy/float64(n)) > 1000 {
				audible++
			}
		case <-deadline.C:
			t.Fatalf("%s: voice timed out after %d frames without a terminator", direction, received)
		}
	}
}
