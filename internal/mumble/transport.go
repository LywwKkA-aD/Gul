package mumble

import "sync"

// Transport names the connection implementation in saved settings and diagnostics.
type Transport string

const TransportHysteria Transport = "hysteria"

// transportChooser keeps the persisted transport hint compatible with older
// settings while allowing only the embedded Hysteria client to carry traffic.
// There is deliberately no fallback to a direct or legacy relay connection.
type transportChooser struct {
	mu    sync.Mutex
	known map[string]Transport
}

func newTransportChooser() *transportChooser {
	return &transportChooser{known: make(map[string]Transport)}
}

func (c *transportChooser) next(_ endpointKind, _ string) Transport {
	return TransportHysteria
}

func (c *transportChooser) succeeded(address string, transport Transport) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.known[address] == transport {
		return false
	}
	c.known[address] = transport
	return true
}

func (c *transportChooser) prefer(_ endpointKind, address string, transport Transport) {
	if transport != TransportHysteria {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known[address] = transport
}

func (c *transportChooser) failed(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.known, address)
}
