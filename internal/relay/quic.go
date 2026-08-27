package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// A second way in, over UDP.
//
// The WebSocket tunnel and this one carry the same bytes to the same Murmur
// and authenticate with the same credential; what differs is the road. A
// network that drops one does not necessarily drop the other, and which one
// works is not something a user should have to know - the client tries and
// keeps whichever actually carries packets.
//
// What this is not: a quality improvement. One reliable QUIC stream has the
// same head-of-line blocking as TCP, so a lost packet still stalls what is
// behind it. Carrying voice in QUIC datagrams would change that, and it is a
// different piece of work in the gumble fork, not here. What QUIC buys today
// is a different road, better loss recovery, and surviving a change of address.
const (
	// quicPreambleTimeout bounds how long a connection may stay open without
	// saying who it is. Long enough for a slow link, short enough that opening
	// connections and going quiet costs the opener more than it costs us.
	quicPreambleTimeout = 10 * time.Second
	// quicKeepAlive keeps the NAT binding that carries a voice session alive
	// through a silence. quicMaxIdle is the matching giving-up point.
	quicKeepAlive = 15 * time.Second
	quicMaxIdle   = 60 * time.Second
)

// QUICServer accepts tunnel connections over QUIC and hands each one to the
// same session pump the WebSocket path uses.
type QUICServer struct {
	handler   *Handler
	listener  *quic.Listener
	transport *quic.Transport
	packets   *relayproto.ObfuscatedPacketConn
	logger    *slog.Logger
}

// ListenQUIC opens the UDP listener. The certificate comes from the same
// loader the HTTPS listener uses, so one renewal covers both.
func ListenQUIC(
	address string,
	getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error),
	handler *Handler,
	logger *slog.Logger,
) (*QUICServer, error) {
	if handler == nil {
		return nil, errors.New("relay QUIC listener needs a handler")
	}
	packets, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, err
	}
	// Every datagram is scrambled, so what leaves this socket has no shape to
	// recognise and what arrives without the password is discarded in silence
	// (relayproto.Salamander). QUIC never sees the socket itself.
	scrambled := relayproto.ObfuscatePacketConn(packets, handler.obfuscator)
	transport := &quic.Transport{Conn: scrambled}
	listener, err := transport.Listen(&tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: getCertificate,
		NextProtos:     []string{relayproto.QUICALPN},
	}, quicConfig())
	if err != nil {
		_ = packets.Close()
		return nil, err
	}
	return &QUICServer{
		handler:   handler,
		listener:  listener,
		transport: transport,
		packets:   scrambled,
		logger:    loggerOrDefault(logger),
	}, nil
}

// quicConfig is the tuning both ends share. The packet size is held down and
// path MTU discovery is off because the salt rides on top of every datagram:
// a packet sized to the path exactly would leave eight bytes over it, and
// discovery would find the real limit and then exceed it on every packet after.
func quicConfig() *quic.Config {
	return &quic.Config{
		MaxIdleTimeout:          quicMaxIdle,
		KeepAlivePeriod:         quicKeepAlive,
		InitialPacketSize:       relayproto.QUICPacketSize,
		DisablePathMTUDiscovery: true,
	}
}

// Addr reports where the listener ended up, which matters when the caller
// asked for port zero.
func (s *QUICServer) Addr() net.Addr { return s.listener.Addr() }

// Serve accepts until ctx is done or the listener is closed.
func (s *QUICServer) Serve(ctx context.Context) error {
	for {
		conn, err := s.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, quic.ErrServerClosed) {
				return nil
			}
			return err
		}
		go s.serveConn(conn)
	}
}

// Close stops accepting and releases the socket. Sessions already running end
// with the handler's own shutdown, which is what owns the drain window.
//
// The UDP socket is closed explicitly: quic.Transport.Close does not close a
// Conn it was handed rather than opened itself, so without this the file
// descriptor and the bound port leak for the life of the process, and a
// rebind to the same port after a restart fails.
func (s *QUICServer) Close() error {
	return errors.Join(s.listener.Close(), s.transport.Close(), s.packets.Close())
}

// serveConn authenticates one connection and runs its tunnel.
//
// One connection carries one tunnel. A peer that opens a second stream is not
// a client of ours, and there is nothing useful to do with it.
func (s *QUICServer) serveConn(conn *quic.Conn) {
	defer func() { _ = conn.CloseWithError(0, "") }()

	sourceIP := remoteIP(conn.RemoteAddr().String())
	sourceBlock := sourceKey(sourceIP)
	if retryAfter, banned := s.handler.authFailures.banRemaining(sourceBlock); banned {
		s.logger.Debug("relay quic connection rate limited",
			"source", sourceIP, "retry_after", retryAfter)
		return
	}

	ctx, cancel := context.WithTimeout(s.handler.ctx, quicPreambleTimeout)
	defer cancel()
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		s.logger.Debug("relay quic stream never opened", "source", sourceIP, "error", err)
		return
	}
	_ = stream.SetReadDeadline(time.Now().Add(quicPreambleTimeout))
	credential, err := relayproto.ReadQUICPreamble(stream)
	if err != nil {
		s.handler.authFailures.recordFailure(sourceBlock)
		s.logger.Warn("relay quic preamble rejected", "source", sourceIP, "error", err)
		return
	}
	_ = stream.SetReadDeadline(time.Time{})

	result := s.handler.authorize(credential.Header())
	if !result.authorized {
		s.handler.authFailures.recordFailure(sourceBlock)
		// The credential itself is never logged, only what class it was.
		s.logger.Warn("relay quic authorization rejected",
			"source", sourceIP, "credential", result.class)
		return
	}
	if _, banned := s.handler.authFailures.clearIfAllowed(sourceBlock); banned {
		return
	}

	release, scope, ok := s.handler.acquire(sourceBlock)
	if !ok {
		s.logger.Warn("relay capacity rejected",
			"source", sourceIP, "transport", "quic", "scope", scope)
		return
	}
	defer release()

	// Chaff in this direction too. Shaping only what the client sends would
	// leave the other half of the conversation a metronome, and an observer
	// sitting next to the user sees both (relayproto.Salamander).
	chaffCtx, stopChaff := context.WithCancel(s.handler.ctx)
	defer stopChaff()
	go s.packets.SendChaff(chaffCtx, conn.RemoteAddr())

	tunnel := &quicStreamConn{Stream: stream, conn: conn}
	if !s.handler.registerStream(tunnel) {
		return
	}
	defer s.handler.unregisterStream(tunnel)

	s.handler.pumpSession(tunnel, sourceIP, sourceBlock, "quic", contractPadded, nil)
}

// quicStreamConn makes one bidirectional stream a net.Conn, which is all the
// rest of the relay - and all of the client - already knows how to speak.
// A stream carries every method net.Conn needs except the addresses, and those
// belong to the connection it rides on.
type quicStreamConn struct {
	*quic.Stream
	conn *quic.Conn
}

func (c *quicStreamConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *quicStreamConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }
