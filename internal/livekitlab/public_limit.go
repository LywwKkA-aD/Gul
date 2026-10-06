package livekitlab

import (
	"net/netip"
	"sync"
	"time"
)

type loginWindow struct {
	start time.Time
	count int
}
type publicLoginLimits struct {
	mu      sync.Mutex
	now     func() time.Time
	global  loginWindow
	clients map[netip.Addr]loginWindow
}

func newPublicLoginLimits() *publicLoginLimits {
	return &publicLoginLimits{now: time.Now, clients: make(map[netip.Addr]loginWindow)}
}

func (l *publicLoginLimits) allow(ip netip.Addr) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for client, window := range l.clients {
		if now.Sub(window.start) >= time.Minute {
			delete(l.clients, client)
		}
	}
	if now.Sub(l.global.start) >= time.Minute {
		l.global = loginWindow{start: now}
	}
	window, exists := l.clients[ip]
	if window.count >= 20 || l.global.count >= 120 || !exists && len(l.clients) >= 1024 {
		return false
	}
	if !exists {
		window.start = now
	}
	window.count++
	l.global.count++
	l.clients[ip] = window
	return true
}
