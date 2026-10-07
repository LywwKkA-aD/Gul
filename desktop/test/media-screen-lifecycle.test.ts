import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { Track, type Room } from 'livekit-client';
import { MediaController } from '../src/renderer/media/controller.ts';
import type { MediaGrant, MediaSession } from '../src/shared/contracts.ts';

const voiceGrant: MediaGrant = {
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
  identity: 'voice.7',
  name: 'Alice',
  channelId: 0,
  revision: 1,
  grant: voiceGrant,
};
const screenGrant = { ...voiceGrant, identity: 'screen.7' };
const deferred = () => {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
class TrackFixture extends EventEmitter {
  readonly kind: Track.Kind;
  readonly mediaStreamTrack = { enabled: true, readyState: 'live', applyConstraints: async () => {} };
  stopped = false;
  constructor(kind: Track.Kind) {
    super();
    this.kind = kind;
  }
  stop() {
    this.stopped = true;
  }
  async mute() {}
  async unmute() {}
}
class RoomFixture extends EventEmitter {
  readonly options = { webAudioMix: true };
  readonly remoteParticipants = new Map();
  disconnected = false;
  unpublishGate?: Promise<void>;
  disconnectGate?: Promise<void>;
  readonly localParticipant = {
    publishTrack: async () => ({ trackSid: 'fixture-video' }),
    unpublishTrack: async () => {
      await this.unpublishGate;
    },
  };
  async connect() {}
  async disconnect() {
    this.disconnected = true;
    await this.disconnectGate;
  }
  async startAudio() {}
  async switchActiveDevice() {
    return true;
  }
}
function fixture() {
  const rooms: RoomFixture[] = [];
  const captures: TrackFixture[] = [];
  const controller = new MediaController({
    screenGrant: async () => screenGrant,
    audioState: async (state) => state,
    roomFactory: () => {
      const room = new RoomFixture();
      rooms.push(room);
      return room as unknown as Room;
    },
    micFactory: async () => new TrackFixture(Track.Kind.Audio) as any,
    captureFactory: async () => {
      const track = new TrackFixture(Track.Kind.Video);
      captures.push(track);
      return { tracks: [track] as any };
    },
    audioElementFactory: () => ({ muted: false, remove() {}, play: async () => {} }) as any,
  });
  return { rooms, captures, controller };
}

test('solo stop/start serializes old unpublish and disconnect before accepting another capture', async () => {
  const { rooms, captures, controller } = fixture();
  await controller.join(session);
  await controller.startScreen(screenGrant, false);
  const unpublish = deferred();
  const disconnect = deferred();
  rooms[1].unpublishGate = unpublish.promise;
  rooms[1].disconnectGate = disconnect.promise;
  const stopping = controller.stopScreen();
  assert.equal(controller.getSnapshot().sharing, false);
  assert.equal(controller.getSnapshot().pendingShare, true);
  await controller.startScreen(screenGrant, false);
  assert.equal(captures.length, 1, 'no capture can publish into the room being unpublished');
  unpublish.resolve();
  await new Promise((done) => setImmediate(done));
  assert.equal(rooms[1].disconnected, true);
  assert.equal(controller.getSnapshot().pendingShare, true, 'the same participant is still disconnecting');
  disconnect.resolve();
  await stopping;
  assert.equal(controller.getSnapshot().pendingShare, false);
  await controller.startScreen(screenGrant, false);
  assert.equal(captures.length, 2);
  assert.equal(rooms[2].disconnected, false);
  assert.equal(controller.getSnapshot().sharing, true);
  await controller.leave();
});

test('late stop cleanup cannot close a new channel screen room or overwrite its pending state', async () => {
  const { rooms, controller } = fixture();
  await controller.join(session);
  await controller.startScreen(screenGrant, false);
  const unpublish = deferred();
  rooms[1].unpublishGate = unpublish.promise;
  const stopping = controller.stopScreen();
  await controller.join({ ...session, epoch: 2 });
  await controller.startScreen(screenGrant, false);
  const current = rooms.at(-1)!;
  unpublish.resolve();
  await stopping;
  assert.equal(current.disconnected, false);
  assert.equal(controller.getSnapshot().sharing, true);
  await controller.leave();
});

test('stopping a watched screen cannot reopen publication while the same-epoch share is still closing', async () => {
  const { rooms, captures, controller } = fixture();
  await controller.join(session);
  await controller.startScreen(screenGrant, false);
  await controller.watchScreen('screen.8');
  const unpublish = deferred();
  const disconnect = deferred();
  rooms[1].unpublishGate = unpublish.promise;
  rooms[1].disconnectGate = disconnect.promise;
  const stop = controller.stopScreen();
  assert.equal(controller.getSnapshot().pendingShare, true);
  const stoppedViewing = controller.watchScreen(null);
  assert.equal(controller.getSnapshot().pendingShare, true);
  await controller.startScreen(screenGrant, false);
  assert.equal(captures.length, 1);
  disconnect.resolve();
  await new Promise((done) => setImmediate(done));
  assert.equal(controller.getSnapshot().pendingShare, true, 'disconnect cannot forget outstanding unpublish');
  unpublish.resolve();
  await Promise.all([stop, stoppedViewing]);
  assert.equal(controller.getSnapshot().pendingShare, false);
  await controller.startScreen(screenGrant, false);
  assert.equal(captures.length, 2);
  assert.equal(controller.getSnapshot().sharing, true);
  await controller.leave();
});
