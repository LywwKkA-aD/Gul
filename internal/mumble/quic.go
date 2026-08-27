package mumble

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/LywwKkA-aD/Gul/internal/relayproto"
)

// The second road to the relay: the same bytes, the same credential, over UDP.
//
// A network that drops one transport does not necessarily drop the other, and
// which one works is not something a user should have to know. Choosing
// between them automatically is the next piece of work; this is the road
// itself.
//
// Not a quality improvement: one reliable QUIC stream blocks head-of-line
// exactly like TCP, so a lost packet still stalls what is behind it. Carrying
// voice in QUIC datagrams would change that and belongs in the gumble fork.
// What this buys is a different road, better loss recovery, and a session that
// survives a change of address.
const (
	// quicDefaultPort is where the relay listens when the address does not
	// say. The same number as HTTPS, because that is the port a network is
	// least willing to block wholesale.
	quicDefaultPort = "443"
	// quicKeepAlive holds the NAT binding open through a silence; quicMaxIdle
	// is the matching giving-up point. Both mirror the relay's own.
	quicKeepAlive = 15 * time.Second
	quicMaxIdle   = 60 * time.Second
)

// dialQUIC opens the tunnel and returns its stream as an ordinary connection.
// Everything above this - the packet framing, the inner Mumble TLS, gumble -
// works the same on either road.
func dialQUIC(
	ctx context.Context,
	address string,
	credential relayproto.Credential,
	roots *tls.Config,
) (net.Conn, error) {
	if credential == "" {
		return nil, ErrRelayPasswordRequired
	}
	target, host, err := quicTarget(address)
	if err != nil {
		return nil, err
	}
	config := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: host,
		NextProtos: []string{relayproto.QUICALPN},
	}
	if roots != nil {
		config.RootCAs = roots.RootCAs
		config.InsecureSkipVerify = roots.InsecureSkipVerify
		if roots.ServerName != "" {
			config.ServerName = roots.ServerName
		}
	}
	remote, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, fmt.Errorf("QUIC relay address: %w", err)
	}
	socket, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, fmt.Errorf("QUIC relay socket: %w", err)
	}
	// Every datagram is scrambled under a key both ends reach from the
	// password, so nothing on the way can tell this is QUIC at all
	// (relayproto.Salamander). QUIC never sees the socket itself.
	packets := relayproto.ObfuscatePacketConn(socket, relayproto.NewObfuscator(credential))
	transport := &quic.Transport{Conn: packets}
	conn, err := transport.Dial(ctx, remote, config, quicConfig())
	if err != nil {
		_ = transport.Close()
		_ = socket.Close()
		return nil, fmt.Errorf("QUIC relay handshake failed: %w", err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(0, "")
		_ = transport.Close()
		_ = socket.Close()
		return nil, fmt.Errorf("QUIC relay stream failed: %w", err)
	}
	// The relay refuses a connection that stays anonymous, so state who is
	// calling before anything else goes down the stream.
	if err := relayproto.WriteQUICPreamble(stream, credential); err != nil {
		_ = conn.CloseWithError(0, "")
		_ = transport.Close()
		_ = socket.Close()
		return nil, fmt.Errorf("QUIC relay handshake failed: %w", err)
	}
	// Chaff runs for the life of the tunnel: it is what keeps the rate from
	// being a metronome and silence from looking like silence
	// (relayproto.Salamander). It never delays a real packet - it only adds.
	chaffCtx, stopChaff := context.WithCancel(context.WithoutCancel(ctx))
	go packets.SendChaff(chaffCtx, remote)

	return &quicStream{
		Stream:    stream,
		conn:      conn,
		transport: transport,
		socket:    socket,
		stopChaff: stopChaff,
	}, nil
}

// quicTarget turns the relay address into a UDP target and the name to verify.
func quicTarget(address string) (target, host string, err error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" {
		return "", "", errors.New("invalid relay address")
	}
	port := parsed.Port()
	if port == "" {
		port = quicDefaultPort
	}
	return net.JoinHostPort(parsed.Hostname(), port), parsed.Hostname(), nil
}

// quicStream makes one bidirectional stream a net.Conn. A stream carries every
// method net.Conn needs except the addresses, which belong to the connection
// it rides on; closing it takes the connection with it, because one connection
// carries exactly one tunnel.
type quicStream struct {
	*quic.Stream
	conn      *quic.Conn
	transport *quic.Transport
	socket    io.Closer
	stopChaff context.CancelFunc
}

// quicConfig is the tuning both ends share. The packet size is held down and
// path MTU discovery is off because the salt rides on top of every datagram:
// a packet sized to the path exactly would leave eight bytes over it, and
// discovery would find the real limit and then exceed it on every packet after
// (relayproto.QUICPacketSize).
func quicConfig() *quic.Config {
	return &quic.Config{
		MaxIdleTimeout:          quicMaxIdle,
		KeepAlivePeriod:         quicKeepAlive,
		InitialPacketSize:       relayproto.QUICPacketSize,
		DisablePathMTUDiscovery: true,
	}
}

func (s *quicStream) LocalAddr() net.Addr  { return s.conn.LocalAddr() }
func (s *quicStream) RemoteAddr() net.Addr { return s.conn.RemoteAddr() }

func (s *quicStream) Close() error {
	s.stopChaff()
	err := errors.Join(s.Stream.Close(), s.conn.CloseWithError(0, ""))
	// The socket belongs to this tunnel alone, so it goes with it. Leaving it
	// open would leak a file descriptor per reconnect, and reconnects are the
	// normal case on the networks this exists for.
	return errors.Join(err, s.transport.Close(), s.socket.Close())
}

// dialQUICMumbleTLS opens the tunnel over QUIC and completes the Mumble TLS
// handshake inside it, exactly as the WebSocket path does. The inner handshake
// is what the certificate pin applies to, and it is the same handshake on
// either road - the road cannot see into it.
func dialQUICMumbleTLS(
	ctx context.Context,
	ep endpoint,
	credential relayproto.Credential,
	tofu *TOFUStore,
	certificate *tls.Certificate,
	outerRoots *tls.Config,
) (net.Conn, error) {
	if ep.kind != endpointRelay {
		return nil, errors.New("relay endpoint is required")
	}
	if tofu == nil {
		return nil, errors.New("TOFU store is required")
	}
	stream, err := dialQUIC(ctx, ep.address, credential, outerRoots)
	if err != nil {
		return nil, err
	}
	tlsConfig := tofu.TLSConfig(ep.host)
	// tls.Client infers no hostname from a net.Conn, so the inner handshake is
	// told which name to send and to pin.
	tlsConfig.ServerName = ep.host
	if certificate != nil {
		tlsConfig.Certificates = []tls.Certificate{*certificate}
	}
	inner := tls.Client(stream, tlsConfig)
	if err := inner.HandshakeContext(ctx); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("inner Mumble TLS handshake failed: %w", err)
	}
	return inner, nil
}
