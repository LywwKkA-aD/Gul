import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { RoomEvent, Track, type Room } from 'livekit-client';
import { MediaController } from '../src/renderer/media/controller.ts';
import type { MediaGrant, MediaSession } from '../src/shared/contracts.ts';

const grant: MediaGrant = {
  url: 'ws://127.0.0.1:5000/capability/rtc',
  token: 'fixture',
  identity: 'voice.7',
  ownerIdentity: 'voice.7',
  room: 'gul.0',
  sessionId: 7,
  channelId: 0,
  revision: 1,
};
const session: MediaSession = {
  epoch: 1,
  sessionId: 7,
  identity: grant.identity,
  name: 'Fixture',
  channelId: 0,
  revision: 1,
  grant,
};
class RoomFixture extends EventEmitter {
  readonly options = { webAudioMix: true };
  readonly remoteParticipants = new Map();
  disconnected = false;
  readonly localParticipant = {
    async publishTrack() {
      return { trackSid: 'fixture-own-video' };
    },
    async unpublishTrack() {},
  };
  async connect() {}
  async disconnect() {
    this.disconnected = true;
  }
  async startAudio() {}
}
function publication(source: Track.Source, sid: string) {
  return { source, trackSid: sid, isMuted: false, setSubscribed(_value: boolean) {} };
}
async function fixture(advanceObserver = true) {
  const screenGrant = { ...grant, identity: 'screen.7' };
  const rooms: RoomFixture[] = [];
  const ownVideo = Object.assign(new EventEmitter(), {
    kind: Track.Kind.Video,
    mediaStreamTrack: { readyState: 'live', async applyConstraints() {} },
    stop() {},
  });
  const controller = new MediaController({
    roomFactory: () => {
      const room = new RoomFixture();
      rooms.push(room);
      return room as unknown as Room;
    },
    micFactory: async () => {
      throw new Error('This fixture has no microphone.');
    },
    captureFactory: async () => ({ tracks: [ownVideo] as any }),
    screenGrant: async () => screenGrant,
    audioState: async (state) => state,
    audioElementFactory: () => ({ dataset: {}, remove() {}, async play() {} }) as any,
  });
  await controller.join(session);
  await controller.startScreen(screenGrant, false);
  const oldVideo = publication(Track.Source.ScreenShare, 'old-video');
  const oldAudio = publication(Track.Source.ScreenShareAudio, 'old-audio');
  const newVideo = publication(Track.Source.ScreenShare, 'new-video');
  const newAudio = publication(Track.Source.ScreenShareAudio, 'new-audio');
  const observer = {
    identity: 'screen.8',
    name: 'Peer',
    trackPublications: new Map([
      [oldVideo.trackSid, oldVideo],
      [oldAudio.trackSid, oldAudio],
    ]),
  };
  const media = { ...observer, trackPublications: new Map(observer.trackPublications) };
  rooms[0].remoteParticipants.set(observer.identity, observer);
  rooms[1].remoteParticipants.set(media.identity, media);
  rooms[0].emit(RoomEvent.ParticipantConnected, observer);
  await controller.watchScreen(observer.identity);

  // Each independent signal flow remains ordered. The observer sees the remote
  // restart while our own capture keeps the media room alive with old metadata.
  if (advanceObserver) {
    observer.trackPublications.clear();
    rooms[0].emit(RoomEvent.TrackUnpublished, oldVideo, observer);
    observer.trackPublications.set(newVideo.trackSid, newVideo);
    observer.trackPublications.set(newAudio.trackSid, newAudio);
    rooms[0].emit(RoomEvent.TrackPublished, newVideo, observer);
    rooms[0].emit(RoomEvent.TrackPublished, newAudio, observer);
    await controller.watchScreen(observer.identity);
    assert.equal(controller.getSnapshot().screens[0].videoSid, newVideo.trackSid);
    assert.equal(controller.getSnapshot().screens[0].audioSid, newAudio.trackSid);
  }
  assert.equal(controller.getSnapshot().screens[0].watching, true);
  return { controller, rooms, observer, media, oldVideo, oldAudio, newVideo, newAudio };
}

for (const stale of ['video-unpublished', 'audio-unpublished', 'video-muted', 'video-unmuted'] as const)
  test(`a delayed ${stale} cannot change the replacement screen or selected viewing`, async () => {
    const f = await fixture();
    try {
      const before = f.controller.getSnapshot();
      if (stale === 'video-unpublished') f.rooms[1].emit(RoomEvent.TrackUnpublished, f.oldVideo, f.media);
      if (stale === 'audio-unpublished') f.rooms[1].emit(RoomEvent.TrackUnpublished, f.oldAudio, f.media);
      if (stale === 'video-muted') f.rooms[1].emit(RoomEvent.TrackMuted, f.oldVideo, f.media);
      if (stale === 'video-unmuted') f.rooms[0].emit(RoomEvent.TrackUnmuted, f.oldVideo, f.observer);
      await new Promise((done) => setImmediate(done));
      assert.deepEqual(f.controller.getSnapshot().screens, before.screens);
      assert.equal(f.controller.getSnapshot().sharing, true, 'the unrelated own capture stays published');
      assert.equal(f.rooms[1].disconnected, false, 'the current media room stays owned');
    } finally {
      await f.controller.leave();
    }
  });

test('matching video and audio events still pause, resume and stop the selected screen', async () => {
  const f = await fixture();
  try {
    f.newVideo.isMuted = true;
    f.rooms[0].emit(RoomEvent.TrackMuted, f.newVideo, f.observer);
    assert.equal(f.controller.getSnapshot().screens[0].state, 'paused');
    f.newVideo.isMuted = false;
    f.rooms[0].emit(RoomEvent.TrackUnmuted, f.newVideo, f.observer);
    assert.equal(f.controller.getSnapshot().screens[0].state, 'watching');
    f.rooms[0].emit(RoomEvent.TrackUnpublished, f.newAudio, f.observer);
    assert.equal(f.controller.getSnapshot().screens[0].audioSid, undefined);
    f.rooms[0].emit(RoomEvent.TrackUnpublished, f.newVideo, f.observer);
    await new Promise((done) => setImmediate(done));
    assert.equal(f.controller.getSnapshot().screens.length, 0);
    assert.equal(f.controller.getSnapshot().sharing, true);
  } finally {
    await f.controller.leave();
  }
});

function audioTrack() {
  return {
    kind: Track.Kind.Audio,
    attached: false,
    attach() {
      this.attached = true;
    },
    detach() {
      this.attached = false;
    },
    setVolume(_value: number) {},
  };
}

test('a delayed observer departure cannot remove a new screen video or audio owned by the media room', async () => {
  const f = await fixture(false);
  try {
    f.media.trackPublications.clear();
    f.rooms[1].remoteParticipants.delete(f.media.identity);
    f.rooms[1].emit(RoomEvent.ParticipantDisconnected, f.media);
    const replacement = {
      ...f.media,
      sid: 'new-media-participant',
      trackPublications: new Map([
        [f.newVideo.trackSid, f.newVideo],
        [f.newAudio.trackSid, f.newAudio],
      ]),
    };
    f.rooms[1].remoteParticipants.set(replacement.identity, replacement);
    f.rooms[1].emit(RoomEvent.ParticipantConnected, replacement);
    f.rooms[1].emit(RoomEvent.TrackSubscribed, { kind: Track.Kind.Video }, f.newVideo, replacement);
    const audio = audioTrack();
    f.rooms[1].emit(RoomEvent.TrackSubscribed, audio, f.newAudio, replacement);
    assert.equal(f.controller.getSnapshot().videos.filter((video) => !video.local).length, 1);
    assert.equal(audio.attached, true);
    f.observer.trackPublications.clear();
    f.rooms[0].remoteParticipants.delete(f.observer.identity);
    f.rooms[0].emit(RoomEvent.ParticipantDisconnected, f.observer);
    const observer = { ...replacement, sid: 'new-observer-participant' };
    f.rooms[0].remoteParticipants.set(observer.identity, observer);
    f.rooms[0].emit(RoomEvent.ParticipantConnected, observer);
    assert.equal(f.controller.getSnapshot().screens[0].videoSid, f.newVideo.trackSid);
    assert.equal(f.controller.getSnapshot().screens[0].watching, true);
    assert.equal(f.controller.getSnapshot().videos.filter((video) => !video.local).length, 1);
    assert.equal(audio.attached, true);
    f.rooms[1].remoteParticipants.delete(replacement.identity);
    f.rooms[1].emit(RoomEvent.ParticipantDisconnected, replacement);
    assert.equal(f.controller.getSnapshot().videos.filter((video) => !video.local).length, 0);
    assert.equal(audio.attached, false, 'the owning media departure still clears its tracks');
  } finally {
    await f.controller.leave();
  }
});

test('a media-room voice participant departure cannot detach actual voice playback from the voice room', async () => {
  const f = await fixture();
  try {
    const pub = publication(Track.Source.Microphone, 'new-voice');
    const voice = { identity: 'voice.8', trackPublications: new Map([[pub.trackSid, pub]]) };
    const audio = audioTrack();
    f.rooms[0].remoteParticipants.set(voice.identity, voice);
    f.rooms[0].emit(RoomEvent.TrackSubscribed, audio, pub, voice);
    assert.equal(audio.attached, true);
    f.rooms[1].emit(RoomEvent.ParticipantDisconnected, { ...voice, trackPublications: new Map() });
    assert.equal(audio.attached, true);
    assert.equal(f.controller.getSnapshot().state, 'connected');
    assert.equal(f.controller.getSnapshot().sharing, true);
    f.rooms[0].remoteParticipants.delete(voice.identity);
    f.rooms[0].emit(RoomEvent.ParticipantDisconnected, voice);
    assert.equal(audio.attached, false, 'the owning voice departure still detaches playback');
  } finally {
    await f.controller.leave();
  }
});

test('a delayed old media participant disconnect cannot delete replacement observer metadata', async () => {
  const f = await fixture();
  try {
    const before = f.controller.getSnapshot();
    // LiveKit clears a disconnecting participant's publications before emitting
    // ParticipantDisconnected, so there is no old SID left on that participant.
    for (const pub of [...f.media.trackPublications.values()]) {
      f.media.trackPublications.delete(pub.trackSid);
      f.rooms[1].emit(RoomEvent.TrackUnpublished, pub, f.media);
    }
    f.rooms[1].remoteParticipants.delete(f.media.identity);
    f.rooms[1].emit(RoomEvent.ParticipantDisconnected, f.media);
    await new Promise((done) => setImmediate(done));
    assert.deepEqual(f.controller.getSnapshot().screens, before.screens);
    assert.equal(f.controller.getSnapshot().sharing, true);
    assert.equal(f.rooms[1].disconnected, false);
    f.rooms[0].remoteParticipants.delete(f.observer.identity);
    f.rooms[0].emit(RoomEvent.ParticipantDisconnected, f.observer);
    assert.equal(
      f.controller.getSnapshot().screens.length,
      0,
      'authoritative observer departure still clears',
    );
  } finally {
    await f.controller.leave();
  }
});
