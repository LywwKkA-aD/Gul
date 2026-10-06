package livekit

import (
	"context"
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/session"
	lk "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestReplacedOrClosedRemoteTrackCannotEmitIntoCurrentStream(t *testing.T) {
	var received []session.VoicePacket
	c := &sdkMedia{ctx: context.Background(), streams: make(map[uint32]*remoteStream), hooks: mediaHooks{packet: func(p session.VoicePacket) { received = append(received, p) }}}
	old := c.startStream(17)
	c.streamPacket(17, old, session.VoicePacket{Session: 17, Sequence: 1})
	current := c.startStream(17)
	c.streamPacket(17, old, session.VoicePacket{Session: 17, Final: true})
	c.streamPacket(17, current, session.VoicePacket{Session: 17, Sequence: 2})
	current.cancel()
	c.streamPacket(17, current, session.VoicePacket{Session: 17, Final: true})
	if len(received) != 2 || received[0].Sequence != 1 || received[1].Sequence != 2 {
		t.Fatalf("old track escaped: %+v", received)
	}
	c.closed = true
	if got := c.startStream(18); got != nil {
		t.Fatal("closed media started reader")
	}
}

func TestOnlyRemoteOwnerBoundAudioSourcesAreSubscribed(t *testing.T) {
	for _, tc := range []struct {
		identity string
		kind     lksdk.TrackKind
		source   lk.TrackSource
		want     bool
	}{
		{"voice.8", lksdk.TrackKindAudio, lk.TrackSource_MICROPHONE, true},
		{"screen.8", lksdk.TrackKindAudio, lk.TrackSource_SCREEN_SHARE_AUDIO, true},
		{"voice.7", lksdk.TrackKindAudio, lk.TrackSource_MICROPHONE, false},
		{"screen.7", lksdk.TrackKindAudio, lk.TrackSource_SCREEN_SHARE_AUDIO, false},
		{"screen.8", lksdk.TrackKindVideo, lk.TrackSource_SCREEN_SHARE, false},
		{"voice.8", lksdk.TrackKindVideo, lk.TrackSource_MICROPHONE, false},
		{"screen.8", lksdk.TrackKindAudio, lk.TrackSource_MICROPHONE, false},
		{"voice.8", lksdk.TrackKindAudio, lk.TrackSource_SCREEN_SHARE_AUDIO, false},
		{"screen.08", lksdk.TrackKindAudio, lk.TrackSource_SCREEN_SHARE_AUDIO, false},
	} {
		if got := acceptSource(7, tc.identity, tc.kind, tc.source); got != tc.want {
			t.Fatalf("source selection for %s/%v/%v: %v", tc.identity, tc.kind, tc.source, got)
		}
	}
}
