package livekittransport

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"time"

	"golang.org/x/net/netutil"
)

func (g *Gateway) acceptTURN() {
	listener := netutil.LimitListener(g.turn, 32)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		g.mu.Lock()
		epoch, ctx := g.epoch, g.mediaCtx
		g.mu.Unlock()
		local := g.track(conn, nil)
		if local == nil {
			return
		}
		if ctx == nil || !g.bindEpoch(epoch, local) {
			_ = local.Close()
			continue
		}
		if !g.work() {
			_ = local.Close()
			return
		}
		go func() { defer g.wg.Done(); g.forwardTURN(ctx, epoch, local) }()
	}
}

// Only a TURN/STUN handshake can use this fixed-destination listener. TURN
// allocations still require credentials from the authenticated signal Join.
func (g *Gateway) forwardTURN(ctx context.Context, epoch uint64, local net.Conn) {
	defer local.Close()
	_ = local.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 20)
	if _, err := io.ReadFull(local, header); err != nil {
		return
	}
	kind := binary.BigEndian.Uint16(header[:2])
	if (kind != 0x0001 && kind != 0x0003) || binary.BigEndian.Uint32(header[4:8]) != 0x2112a442 || binary.BigEndian.Uint16(header[2:4])%4 != 0 {
		return
	}
	remote, err := g.dialTLS(ctx, "tcp", g.upstreamAddress())
	if err != nil {
		return
	}
	defer remote.Close()
	tracked, ok := remote.(*trackedConn)
	if !ok || !g.bindEpoch(epoch, tracked) {
		return
	}
	_ = remote.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := remote.Write(header); err != nil {
		return
	}
	_ = remote.SetWriteDeadline(time.Time{})
	_ = local.SetReadDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
}
