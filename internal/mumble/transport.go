package mumble

import "sync"

// Transport names the connection implementation in saved settings and diagnostics.
type Transport string

const (
	TransportHysteria Transport = "hysteria"
	TransportReality  Transport = "reality"
)

// transportChooser keeps the persisted transport hint compatible with older
// settings while selecting the embedded transport explicitly named by the URL.
// There is deliberately no fallback to a direct or legacy relay connection.
type transportChooser struct {
	mu    sync.Mutex
	known map[string]Transport
}

func newTransportChooser() *transportChooser {
	return &transportChooser{known: make(map[string]Transport)}
}

func (c *transportChooser) next(kind endpointKind, _ string) Transport {
	if kind == endpointReality {
		return TransportReality
	}
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

func (c *transportChooser) prefer(kind endpointKind, address string, transport Transport) {
	if transport != c.next(kind, address) {
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
