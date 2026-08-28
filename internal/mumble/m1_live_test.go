//go:build live

package mumble

import (
	"crypto/tls"
	"log/slog"
	"math"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// TestTwoManagersChat is the M1 DoD smoke at the protocol layer: two client
// instances against the local dev stand exchange a message.
// Run with the stand up: task murmur:up && go test -tags live ./internal/mumble -run TestTwoManagersChat
func TestTwoManagersChat(t *testing.T) {
	a := newLiveManager(t, "gul-live-a")
	defer a.mgr.Close()
	b := newLiveManager(t, "gul-live-b")
	defer b.mgr.Close()

	connectLive(t, a, "gul-live-a")
	connectLive(t, b, "gul-live-b")
	waitState(t, a, domain.StateConnected)
	waitState(t, b, domain.StateConnected)

	// Both land in the root channel; find its id from B's tree snapshot.
	rootID := waitRoot(t, b)

	const probe = "hello from A (m1 live smoke)"
	if err := a.mgr.SendMessage(rootID, probe); err != nil {
		t.Fatalf("send: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range b.messages() {
			if m.HTML == probe && m.Sender == "gul-live-a" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("B never received A's message; got: %+v", b.messages())
}

// TestManagerReportsLatency verifies the latency path the UI shows. The number
// is the client's own round trip on the already-open TLS/TCP session (not a
// separate UDP ping, and not the server's session-long average, which never
// decays). On the loopback stand it has to be small - a server-side average
// would also pass a "is it a number" check, so the bound is what makes this
// test able to tell the two apart.
func TestManagerReportsLatency(t *testing.T) {
	c := newLiveManager(t, "gul-latency")
	defer c.mgr.Close()

	connectLive(t, c, "gul-latency")
	waitState(t, c, domain.StateConnected)

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if latency, ok := c.connectionLatency(); ok {
			if math.IsNaN(latency.PingMS) || math.IsInf(latency.PingMS, 0) || latency.PingMS < 0 {
				t.Fatalf("invalid TCP RTT: %f ms", latency.PingMS)
			}
			if latency.PingMS > 100 {
				t.Fatalf("TCP RTT %.2f ms on loopback: too high for a fresh sample", latency.PingMS)
			}
			t.Logf("TCP RTT: %.2f ms", latency.PingMS)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no TCP RTT sample arrived")
}

type liveClient struct {
	mgr *Manager
	mu  sync.Mutex
	st  domain.ConnectionStatus
	lat *domain.ConnectionLatency
	tr  *domain.ChannelNode
	msg []RawMessage
}

func newLiveManager(t *testing.T, name string) *liveClient {
	t.Helper()
	c := &liveClient{}
	mgr, err := NewManager(t.TempDir(), slog.Default().With("client", name), Callbacks{
		OnStatus: func(s domain.ConnectionStatus) { c.mu.Lock(); c.st = s; c.mu.Unlock() },
		OnLatency: func(latency domain.ConnectionLatency) {
			c.mu.Lock()
			c.lat = &latency
			c.mu.Unlock()
		},
		OnTree: func(n domain.ChannelNode) { c.mu.Lock(); c.tr = &n; c.mu.Unlock() },
		OnMessage: func(m RawMessage) {
			c.mu.Lock()
			c.msg = append(c.msg, m)
			c.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.mgr = mgr
	return c
}

// liveRelay is the address every live client now connects to, and the password
// that opens it.
//
// The direct road is gone: there is no way to reach Murmur from this client
// except through a relay, which is exactly the shape production has. So the
// stand gets one, standing in front of the same Murmur these tests always
// used. Its certificate is signed by nobody the machine trusts, hence the
// roots handed to the manager (DialConfig.OuterRoots).
func liveRelay(t *testing.T) (address, password string, roots *tls.Config) {
	t.Helper()
	ep, wssRoots, _ := localRelay(t)
	return ep.address, relayLiveSecret, wssRoots
}

// connectLive points one live client at the stand's relay.
func connectLive(t *testing.T, c *liveClient, name string) {
	t.Helper()
	address, password, roots := liveRelay(t)
	c.mgr.outerRoots = roots
	c.mgr.Connect(address, name, password)
}

func (c *liveClient) connectionLatency() (domain.ConnectionLatency, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lat == nil {
		return domain.ConnectionLatency{}, false
	}
	return *c.lat, true
}

func (c *liveClient) state() domain.ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st.State
}

func (c *liveClient) messages() []RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]RawMessage(nil), c.msg...)
}

func waitState(t *testing.T, c *liveClient, want domain.ConnState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c.state() == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("state never reached %s (last: %s)", want, c.state())
}

func waitRoot(t *testing.T, c *liveClient) uint32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		tr := c.tr
		c.mu.Unlock()
		if tr != nil {
			return tr.ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("tree snapshot never arrived")
	return 0
}

// TestReconnectAfterServerRestart is the M1 DoD smoke for the reconnect loop:
// kill the server mid-session and watch the manager come back on its own.
// Needs docker and the dev stand (task murmur:up).
func TestReconnectAfterServerRestart(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	c := newLiveManager(t, "gul-reconnect")
	defer c.mgr.Close()
	connectLive(t, c, "gul-reconnect")
	waitState(t, c, domain.StateConnected)

	cmd := exec.Command("docker", "compose", "-f", "../../deploy/murmur/docker-compose.yml", "restart", "mumble")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart server: %v\n%s", err, out)
	}

	waitState(t, c, domain.StateReconnecting)
	// The stand needs a few seconds to come back; the manager retries with
	// backoff, give it a generous window.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if c.state() == domain.StateConnected {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("never reconnected, last state: %s", c.state())
}

// TestChannelWalk covers the "walk between channels" half of the M1 DoD:
// SuperUser creates a persistent channel, two clients join it and talk there.
// The channel stays on the stand afterwards - handy for manual checks too.
func TestChannelWalk(t *testing.T) {
	admin := newLiveManager(t, "SuperUser")
	defer admin.mgr.Close()
	// SuperUser needs its own password, and the relay was told about it.
	adminAddress, _, adminRoots := liveRelay(t)
	admin.mgr.outerRoots = adminRoots
	admin.mgr.Connect(adminAddress, "SuperUser", liveSuperUserPassword)
	waitState(t, admin, domain.StateConnected)

	if findChannelByName(admin.tree(), "general") == nil {
		admin.createChannel("general")
	}
	generalID := waitChannel(t, admin, "general")

	a := newLiveManager(t, "gul-walk-a")
	defer a.mgr.Close()
	b := newLiveManager(t, "gul-walk-b")
	defer b.mgr.Close()
	connectLive(t, a, "gul-walk-a")
	connectLive(t, b, "gul-walk-b")
	waitState(t, a, domain.StateConnected)
	waitState(t, b, domain.StateConnected)

	if err := a.mgr.Join(generalID); err != nil {
		t.Fatalf("A join general: %v", err)
	}
	if err := b.mgr.Join(generalID); err != nil {
		t.Fatalf("B join general: %v", err)
	}
	// Join is async on the server side: send only after both moves landed,
	// otherwise the message is delivered to a channel B is not yet in.
	waitSelfInChannel(t, a, generalID)
	waitSelfInChannel(t, b, generalID)

	const probe = "meet me in general"
	if err := a.mgr.SendMessage(generalID, probe); err != nil {
		t.Fatalf("send: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range b.messages() {
			if m.HTML == probe && m.ChannelID == generalID {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("B never got the message in general: %+v", b.messages())
}

func (c *liveClient) tree() *domain.ChannelNode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tr
}

func (c *liveClient) createChannel(name string) {
	// Channel.Add is fire-and-forget; the new channel arrives as a tree update.
	c.mgr.mu.Lock()
	client := c.mgr.client
	c.mgr.mu.Unlock()
	client.Do(func() {
		client.Channels[0].Add(name, false)
	})
}

func findChannelByName(node *domain.ChannelNode, name string) *domain.ChannelNode {
	if node == nil {
		return nil
	}
	if node.Name == name {
		return node
	}
	for i := range node.Children {
		if hit := findChannelByName(&node.Children[i], name); hit != nil {
			return hit
		}
	}
	return nil
}

func waitChannel(t *testing.T, c *liveClient, name string) uint32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ch := findChannelByName(c.tree(), name); ch != nil {
			return ch.ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("channel %q never appeared", name)
	return 0
}

func waitSelfInChannel(t *testing.T, c *liveClient, channelID uint32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ch := findChannelByID(c.tree(), channelID); ch != nil {
			for _, u := range ch.Users {
				if u.IsSelf {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("self never landed in channel %d", channelID)
}

func findChannelByID(node *domain.ChannelNode, id uint32) *domain.ChannelNode {
	if node == nil {
		return nil
	}
	if node.ID == id {
		return node
	}
	for i := range node.Children {
		if hit := findChannelByID(&node.Children[i], id); hit != nil {
			return hit
		}
	}
	return nil
}
