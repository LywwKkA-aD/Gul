package hysteria

import (
	"net"
	"sync"

	"github.com/apernet/hysteria/extras/v2/obfs"
)

// packetFactory lets setup cancellation interrupt the official client's
// handshake, whose API does not accept a context. It owns one UDP socket.
type packetFactory struct {
	mode     string
	password string
	mu       sync.Mutex
	conn     net.PacketConn
	closed   bool
}

func (f *packetFactory) New(address net.Addr) (net.PacketConn, error) {
	network := "udp4"
	if udp, ok := address.(*net.UDPAddr); ok && udp.IP.To4() == nil {
		network = "udp6"
	}
	udp, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, err
	}
	packet := &ownedPacketConn{UDPConn: udp}
	var conn net.PacketConn = packet
	switch f.mode {
	case "salamander":
		conn, err = obfs.WrapPacketConnSalamander(packet, []byte(f.password))
	case "gecko":
		conn, err = obfs.WrapPacketConnGecko(packet, obfs.GeckoOptions{Password: []byte(f.password)})
	}
	if err != nil {
		_ = packet.Close()
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.conn != nil {
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	f.conn = conn
	return conn, nil
}

// Both core and setup cancellation can own cleanup. Preserve UDP optimizations
// while ensuring they close the underlying socket only once.
type ownedPacketConn struct {
	*net.UDPConn
	once sync.Once
	err  error
}

func (c *ownedPacketConn) Close() error {
	c.once.Do(func() { c.err = c.UDPConn.Close() })
	return c.err
}

func (f *packetFactory) close() {
	f.mu.Lock()
	f.closed = true
	conn := f.conn
	f.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}
