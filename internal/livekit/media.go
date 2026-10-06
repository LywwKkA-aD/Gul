package livekit

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	api "github.com/LywwKkA-aD/Gul/internal/livekitapi"
	"github.com/LywwKkA-aD/Gul/internal/session"
	lk "github.com/livekit/protocol/livekit"
	lklog "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type mediaHooks struct {
	packet    func(session.VoicePacket)
	message   func(session.RawMessage)
	reconnect func()
}
type mediaConnection interface {
	write(*rtp.Packet) error
	chat(string) error
	mute(bool)
	close()
}
type mediaDial func(context.Context, api.Grant, mediaHooks) (mediaConnection, error)

func init() {
	// SDK 2.18.1 builds Pion's logger from this package default, even when a
	// per-room logger is supplied. Initialize before any transport goroutines.
	lksdk.SetLogger(lklog.GetDiscardLogger())
}

type sdkMedia struct {
	room        *lksdk.Room
	track       *lksdk.LocalTrack
	publication *lksdk.LocalTrackPublication
	ctx         context.Context
	cancel      context.CancelFunc
	grant       api.Grant
	hooks       mediaHooks
	mu          sync.Mutex
	closed      bool
	streams     map[uint32]*remoteStream
	wg          sync.WaitGroup
	once        sync.Once
}

type remoteStream struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func participantID(identity string) (uint32, string, bool) {
	role, number, ok := strings.Cut(identity, ".")
	if !ok || (role != "voice" && role != "screen") {
		return 0, "", false
	}
	n, err := strconv.ParseUint(number, 10, 31)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != number {
		return 0, "", false
	}
	return uint32(n), role, true
}

func dialMedia(ctx context.Context, grant api.Grant, hooks mediaHooks) (mediaConnection, error) {
	if !validGrant(grant, false) {
		return nil, ErrBroker
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &sdkMedia{ctx: ctx, cancel: cancel, grant: grant, hooks: hooks, streams: make(map[uint32]*remoteStream)}
	cb := lksdk.NewRoomCallback()
	cb.OnTrackPublished = func(pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		if c.accept(pub, rp) {
			_ = pub.SetSubscribed(true)
		}
	}
	cb.OnTrackSubscribed = c.subscribed
	cb.OnDataPacket = c.receivedData
	cb.OnDisconnected = func() {
		if ctx.Err() == nil {
			hooks.reconnect()
		}
	}
	cb.OnReconnecting = func() {
		if ctx.Err() == nil {
			hooks.reconnect()
		}
	}
	c.room = lksdk.NewRoom(cb)
	c.room.SetLogger(lklog.GetDiscardLogger())
	if err := c.room.JoinWithContextAndToken(ctx, grant.URL, grant.Token,
		lksdk.WithAutoSubscribe(false), lksdk.WithDisableRegionDiscovery(), lksdk.WithDisableTURN(),
		lksdk.WithLogger(lklog.GetDiscardLogger()), lksdk.WithConnectTimeout(15*time.Second)); err != nil {
		c.close()
		return nil, ErrMedia
	}
	track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;stereo=0;usedtx=0"})
	if err != nil {
		c.close()
		return nil, ErrMedia
	}
	track.SetLogger(lklog.GetDiscardLogger())
	c.track = track
	pub, err := c.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{Name: "Gul voice", Source: lk.TrackSource_MICROPHONE, DisableDTX: true, Stereo: false})
	if err != nil {
		c.close()
		return nil, ErrMedia
	}
	c.publication = pub
	for _, rp := range c.room.GetRemoteParticipants() {
		for _, p := range rp.TrackPublications() {
			if pub, ok := p.(*lksdk.RemoteTrackPublication); ok && c.accept(pub, rp) {
				_ = pub.SetSubscribed(true)
			}
		}
	}
	if err := waitBound(ctx, track.IsBound, 15*time.Second); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func waitBound(ctx context.Context, bound func() bool, timeout time.Duration) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for !bound() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrMedia
		case <-ticker.C:
		}
	}
	return nil
}

func (c *sdkMedia) accept(pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) bool {
	return acceptSource(c.grant.SessionID, rp.Identity(), pub.Kind(), pub.Source())
}

func acceptSource(self uint32, identity string, kind lksdk.TrackKind, source lk.TrackSource) bool {
	id, role, ok := participantID(identity)
	return ok && id != self && kind == lksdk.TrackKindAudio && ((role == "voice" && source == lk.TrackSource_MICROPHONE) || (role == "screen" && source == lk.TrackSource_SCREEN_SHARE_AUDIO))
}

func (c *sdkMedia) subscribed(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if !c.accept(pub, rp) || !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeOpus) || track.Codec().ClockRate != 48000 {
		return
	}
	id, role, _ := participantID(rp.Identity())
	stream := id
	if role == "screen" {
		stream |= 0x80000000
	}
	reader := c.startStream(stream)
	if reader == nil {
		return
	}
	ctx := reader.ctx
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		reader.cancel()
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		defer reader.cancel()
		q := newRTPQueue(stream, "s:livekit:"+strconv.FormatUint(uint64(id), 10))
		for ctx.Err() == nil {
			_ = track.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
			packet, _, err := track.ReadRTP()
			now := time.Now()
			if err != nil {
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					break
				}
				for _, p := range q.flush(now) {
					c.streamPacket(stream, reader, p)
				}
				continue
			}
			for _, p := range q.push(packet, now) {
				c.streamPacket(stream, reader, p)
			}
		}
		if q.initialized && !q.ended {
			c.streamPacket(stream, reader, q.finish())
		}
	}()
}

func (c *sdkMedia) startStream(stream uint32) *remoteStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if previous := c.streams[stream]; previous != nil {
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	reader := &remoteStream{ctx: ctx, cancel: cancel}
	c.streams[stream] = reader
	return reader
}

func (c *sdkMedia) streamPacket(id uint32, reader *remoteStream, p session.VoicePacket) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && c.streams[id] == reader && reader.ctx.Err() == nil {
		c.hooks.packet(p)
	}
}

func (c *sdkMedia) receivedData(packet lksdk.DataPacket, params lksdk.DataReceiveParams) {
	data, ok := packet.(*lksdk.UserDataPacket)
	if !ok || data.Topic != "gul.chat.v1" || params.Sender == nil || len(data.Payload) > 24000 {
		return
	}
	id, role, ok := participantID(params.Sender.Identity())
	if !ok || role != "voice" || id == c.grant.SessionID {
		return
	}
	var payload struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data.Payload, &payload) != nil || !utf8.ValidString(payload.Text) || utf8.RuneCountInString(payload.Text) > 5000 || strings.TrimSpace(payload.Text) == "" {
		return
	}
	c.hooks.message(session.RawMessage{ChannelID: c.grant.ChannelID, Sender: params.Sender.Name(), SenderHash: "s:livekit:" + strconv.FormatUint(uint64(id), 10), HTML: html.EscapeString(payload.Text)})
}
func (c *sdkMedia) write(packet *rtp.Packet) error { return c.track.WriteRTP(packet, nil) }
func (c *sdkMedia) chat(text string) error {
	data, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{text})
	if err := c.room.LocalParticipant.PublishDataPacket(&lksdk.UserDataPacket{Payload: data, Topic: "gul.chat.v1"}, lksdk.WithDataPublishReliable(true)); err != nil {
		return ErrMedia
	}
	return nil
}
func (c *sdkMedia) mute(value bool) {
	if c.publication != nil {
		c.publication.SetMuted(value)
	}
}
func (c *sdkMedia) close() {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		for _, reader := range c.streams {
			reader.cancel()
		}
		c.mu.Unlock()
		c.room.Disconnect()
		if c.track != nil {
			_ = c.track.Close()
		}
		c.wg.Wait()
	})
}
