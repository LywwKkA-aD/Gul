import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { ConnectionError, RoomEvent, Track } from 'livekit-client';
import { LiveKitController } from './controller.ts';
import { ScreenGrantError } from './connection.ts';

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

class FakeTrack extends EventEmitter {
  constructor(kind, id) {
    super();
    this.kind = kind;
    this.sid = id;
    this.stops = 0;
    this.constraints = [];
    this.mediaStreamTrack = {
      id, readyState: 'live',
      applyConstraints: async (value) => { this.constraints.push(value); },
    };
  }
  stop() { this.stops++; this.mediaStreamTrack.readyState = 'ended'; }
}

class FakeRoom extends EventEmitter {
  remoteParticipants = new Map();
  published = [];
  unpublished = [];
  disconnects = 0;
  connections = [];
  captures = [];
  localParticipant = {
    publishTrack: async (track, options) => {
      this.published.push({ track, options });
      await this.publishGate?.(track);
      return { trackSid: track.sid, track };
    },
    unpublishTrack: async (track) => { this.unpublished.push(track); },
    createScreenTracks: (options) => {
      this.captures.push(options);
      return Promise.resolve(this.nextCapture);
    },
  };
  connect = async (...args) => { this.connections.push(args); await this.connectGate; };
  disconnect = async () => { this.disconnects++; };
  startAudio = async () => { this.audioStarted = true; };
}

const grant = { url: 'ws://127.0.0.1:7880', token: 'private-test-token', identity: 'alice', room: 'local' };

test('relay-only remote verification preserves server-issued ICE and keeps local ICE fenced', async () => {
  const remote = setup(async () => ({ ...grant, url: 'wss://gul.example' }), { allowServerIce: true, forceRelay: true });
  await remote.controller.join('alice');
  assert.deepEqual(remote.room.connections[0][2].rtcConfig, { iceTransportPolicy: 'relay' });
  await remote.controller.leave();
  const local = setup(undefined, { allowServerIce: true, forceRelay: true });
  await local.controller.join('alice');
  assert.deepEqual(local.room.connections[0][2].rtcConfig, { iceServers: [], iceTransportPolicy: 'relay' });
  await local.controller.leave();
});

test('authenticated REALITY gateway preserves rewritten server ICE and forces relay on loopback', async () => {
  const { controller, room } = setup(async () => ({ ...grant, url: `ws://127.0.0.1:41900/${'a'.repeat(64)}`, transport: 'reality', relayOnly: true }), { allowServerIce: true });
  await controller.join('alice');
  assert.deepEqual(room.connections[0][2].rtcConfig, { iceTransportPolicy: 'relay' });
  await controller.leave();
});
const tick = () => new Promise((resolve) => setImmediate(resolve));
function setup(provider = async () => grant, options = {}) {
  const room = new FakeRoom();
  const controller = new LiveKitController(provider, { runtimeCheck: () => undefined, ...options, roomFactory: () => room });
  return { room, controller };
}
const tracks = () => [new FakeTrack(Track.Kind.Video, 'screen'), new FakeTrack(Track.Kind.Audio, 'sound')];

test('unsupported embedded RTC reports its local cause before requesting a grant', async () => {
  let grants = 0;
  const { room, controller } = setup(async () => { grants++; return grant; }, {
    retryJoin: true, runtimeCheck: () => 'SCREEN_RTC_UNAVAILABLE',
  });
  await controller.join('alice');
  assert.equal(grants, 0);
  assert.equal(room.connections.length, 0);
  assert.match(controller.getSnapshot().error, /SCREEN_RTC_UNAVAILABLE/);
});

test('transient connection failure retries with a fresh grant and room despite SDK disconnect events', async () => {
  let grants = 0;
  const rooms = [];
  const controller = new LiveKitController(async () => ({ ...grant, token: `private-${++grants}` }), {
    runtimeCheck: () => undefined, retryJoin: true, waitForRetry: async () => true,
    roomFactory: () => {
      const room = new FakeRoom();
      rooms.push(room);
      if (rooms.length === 1) room.connect = async () => {
        room.emit(RoomEvent.Disconnected);
        throw ConnectionError.timeout('private-token');
      };
      return room;
    },
  });
  await controller.join('alice');
  assert.equal(grants, 2);
  assert.equal(rooms.length, 2);
  assert.equal(rooms[0].disconnects, 1);
  assert.equal(rooms[1].connections[0][1], 'private-2');
  assert.equal(controller.getSnapshot().status, 'connected');
  assert.equal(controller.getSnapshot().error, '');
  await controller.leave();
});

test('retries stop after three fresh grants and preserve a safe actionable failure code', async () => {
  let grants = 0;
  const { controller, room } = setup(async () => { grants++; return grant; }, {
    retryJoin: true, waitForRetry: async () => true,
  });
  room.connect = async () => { throw ConnectionError.websocket('private-token'); };
  await controller.join('alice');
  assert.equal(grants, 3);
  assert.match(controller.getSnapshot().error, /SCREEN_SIGNAL/);
  assert.ok(!JSON.stringify(controller.getSnapshot()).includes('private-token'));
  assert.equal(controller.getSnapshot().status, 'disconnected');
});

test('leave cancels backoff and cannot request another grant for the old channel', async () => {
  let grants = 0;
  let retrySignal;
  const backoff = deferred();
  const { controller } = setup(async () => { grants++; throw new Error('private-token'); }, {
    retryJoin: true, waitForRetry: (_delay, signal) => { retrySignal = signal; return backoff.promise; },
  });
  const joining = controller.join('alice');
  await tick();
  await controller.leave();
  assert.equal(retrySignal.aborted, true);
  backoff.resolve(true);
  await joining;
  assert.equal(grants, 1);
  assert.equal(controller.getSnapshot().error, '');
});

test('invalid grants and unsupported runtime construction fail once with distinct codes', async () => {
  let grants = 0;
  const invalid = setup(async () => { grants++; throw new ScreenGrantError('SCREEN_GRANT_INVALID'); }, {
    retryJoin: true, waitForRetry: async () => true,
  });
  await invalid.controller.join('alice');
  assert.equal(grants, 1);
  assert.match(invalid.controller.getSnapshot().error, /SCREEN_GRANT_INVALID/);
  let rooms = 0;
  const runtime = new LiveKitController(async () => grant, {
    runtimeCheck: () => undefined, retryJoin: true, waitForRetry: async () => true,
    roomFactory: () => { rooms++; throw new Error('private-token'); },
  });
  await runtime.join('alice');
  assert.equal(rooms, 1);
  assert.match(runtime.getSnapshot().error, /SCREEN_RUNTIME/);
  assert.ok(!runtime.getSnapshot().error.includes('private-token'));
});

test('leave cancels a pending token request without opening a room', async () => {
  const token = deferred();
  const { room, controller } = setup(() => token.promise);
  const joining = controller.join('alice');
  await controller.leave();
  token.resolve(grant);
  await joining;
  assert.equal(room.connections.length, 0);
  assert.equal(controller.getSnapshot().status, 'disconnected');
});

test('a connect that completes after leave is disconnected again and cannot revive state', async () => {
  const { room, controller } = setup();
  const connecting = deferred();
  room.connectGate = connecting.promise;
  const joining = controller.join('alice');
  await tick();
  await controller.leave();
  connecting.resolve();
  await joining;
  assert.equal(room.disconnects, 2);
  assert.equal(controller.getSnapshot().status, 'disconnected');
  room.emit(RoomEvent.Reconnected);
  assert.equal(controller.getSnapshot().status, 'disconnected');
});

test('share opens capture within the caller gesture; late capture after leave is released', async () => {
  const { room, controller } = setup();
  await controller.join('alice');
  const capture = deferred();
  let invoked = false;
  let cleaned = 0;
  const sharing = controller.shareWith(() => { invoked = true; return capture.promise; });
  assert.equal(invoked, true);
  assert.equal(controller.getSnapshot().pendingShare, true);
  await controller.leave();
  const captured = tracks();
  capture.resolve({ tracks: captured, cleanup: () => { cleaned++; } });
  await sharing;
  assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
  assert.equal(cleaned, 1);
  assert.equal(room.published.length, 0);
  assert.equal(controller.getSnapshot().sharing, false);
});

test('capture rejection reports a fixed message without leaking the grant or exception', async () => {
  const { controller } = setup();
  await controller.join('alice');
  await controller.shareWith(async () => { throw new Error(grant.token); });
  assert.equal(controller.getSnapshot().pendingShare, false);
  assert.ok(controller.getSnapshot().error);
  assert.ok(!JSON.stringify(controller.getSnapshot()).includes(grant.token));
});

test('partial publish failure unpublishes and stops both tracks and their producer', async () => {
  const { room, controller } = setup();
  await controller.join('alice');
  room.publishGate = async (track) => {
    if (track.kind === Track.Kind.Audio) throw new Error(grant.token);
  };
  const captured = tracks();
  let cleaned = 0;
  await controller.shareWith(async () => ({ tracks: captured, cleanup: () => { cleaned++; } }));
  assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
  assert.ok(captured.every((track) => room.unpublished.includes(track)));
  assert.equal(cleaned, 1);
  assert.equal(controller.getSnapshot().tracks.length, 0);
  assert.equal(controller.getSnapshot().sharing, false);
  assert.ok(!controller.getSnapshot().error.includes(grant.token));
});

test('stop during a pending publication also removes its late successful publication', async () => {
  const { room, controller } = setup();
  await controller.join('alice');
  const publishing = deferred();
  room.publishGate = () => publishing.promise;
  const captured = tracks();
  const sharing = controller.shareWith(async () => ({ tracks: captured }));
  await tick();
  await controller.stopShare();
  const before = room.unpublished.length;
  publishing.resolve();
  await sharing;
  assert.equal(room.published.length, 1);
  assert.ok(room.unpublished.length > before);
  assert.equal(controller.getSnapshot().sharing, false);
  assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
});

test('ending either captured track stops the whole share and cleans its producer exactly once', async () => {
  for (const endedIndex of [0, 1]) {
    const { room, controller } = setup();
    await controller.join('alice');
    const captured = tracks();
    let cleaned = 0;
    await controller.shareWith(async () => ({ tracks: captured, cleanup: () => { cleaned++; } }));
    assert.equal(controller.getSnapshot().sharing, true);
    assert.equal(controller.getSnapshot().screenAudio, true);
    captured[endedIndex].emit('ended');
    await tick();
    await controller.stopShare();
    assert.equal(cleaned, 1);
    assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
    assert.ok(captured.every((track) => room.unpublished.includes(track)));
  }
});

test('default capture requests only display audio and video, with bounded publish encoding', async () => {
  const { room, controller } = setup();
  room.nextCapture = [tracks()[0]];
  await controller.join('alice');
  const sharing = controller.share();
  assert.equal(room.captures.length, 1);
  await sharing;
  assert.equal(room.captures[0].audio.restrictOwnAudio, true);
  assert.equal(room.captures[0].audio.echoCancellation, false);
  assert.equal(room.captures[0].audio.noiseSuppression, false);
  assert.equal(room.captures[0].audio.autoGainControl, false);
  assert.equal(room.captures[0].systemAudio, 'include');
  assert.equal(room.published[0].options.source, Track.Source.ScreenShare);
  assert.equal(room.published[0].options.screenShareEncoding.maxBitrate, 4_000_000);
  assert.equal(room.published[0].options.screenShareEncoding.maxFramerate, 30);
  assert.equal(room.published[0].options.simulcast, false);
  assert.equal(controller.getSnapshot().screenAudio, false);
  assert.ok(controller.getSnapshot().warning);
  assert.deepEqual(room.nextCapture[0].constraints[0], {
    width: { max: 1920 }, height: { max: 1080 }, frameRate: { max: 30 },
  });
  await controller.leave();
});

test('only remote screen tracks enter stable immutable snapshots and events update lifecycle', async () => {
  const { room, controller } = setup();
  let updates = 0;
  const unsubscribe = controller.subscribe(() => { updates++; });
  await controller.join('alice');
  const initial = controller.getSnapshot();
  assert.equal(initial, controller.getSnapshot());
  assert.equal(Object.isFrozen(initial), true);
  assert.equal(Object.isFrozen(initial.tracks), true);
  const bob = { identity: 'bob', trackPublications: new Map() };
  const video = tracks()[0];
  room.emit(RoomEvent.TrackSubscribed, video, { source: Track.Source.Camera, trackSid: 'camera' }, bob);
  assert.equal(controller.getSnapshot().tracks.length, 0);
  room.remoteParticipants.set('bob', bob);
  room.emit(RoomEvent.ParticipantConnected, bob);
  room.emit(RoomEvent.TrackSubscribed, video, { source: Track.Source.ScreenShare, trackSid: 'remote-screen' }, bob);
  assert.equal(controller.getSnapshot().tracks[0].participant, 'bob');
  assert.equal(controller.getSnapshot().tracks[0].local, false);
  assert.ok(controller.getSnapshot().participants.includes('bob'));
  room.emit(RoomEvent.Reconnecting);
  assert.equal(controller.getSnapshot().status, 'reconnecting');
  room.emit(RoomEvent.Reconnected);
  assert.equal(controller.getSnapshot().status, 'connected');
  room.emit(RoomEvent.TrackUnsubscribed, video, { trackSid: 'remote-screen' }, bob);
  assert.equal(controller.getSnapshot().tracks.length, 0);
  await controller.startAudio();
  assert.equal(room.audioStarted, true);
  await controller.leave();
  const before = updates;
  unsubscribe();
  room.emit(RoomEvent.TrackSubscribed, video, { source: Track.Source.ScreenShare, trackSid: 'stale' }, bob);
  assert.equal(updates, before);
  assert.equal(controller.getSnapshot().tracks.length, 0);
});

test('unexpected room disconnect releases local capture and reports a safe message', async () => {
  const { room, controller } = setup();
  await controller.join('alice');
  const captured = tracks();
  await controller.shareWith(async () => ({ tracks: captured }));
  room.emit(RoomEvent.Disconnected);
  await tick();
  assert.equal(controller.getSnapshot().status, 'disconnected');
  assert.ok(controller.getSnapshot().error);
  assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
});

test('subscribe only to existing and newly published screen media', async () => {
  const { room, controller } = setup();
  const subscriptions = [];
  const publication = (source) => ({ source, setSubscribed: (value) => subscriptions.push({ source, value }) });
  const screen = publication(Track.Source.ScreenShare);
  const microphone = publication(Track.Source.Microphone);
  room.remoteParticipants.set('bob', {
    identity: 'bob', trackPublications: new Map([['screen', screen], ['microphone', microphone]]),
  });
  await controller.join('alice');
  assert.equal(room.connections[0][2].autoSubscribe, false);
  assert.deepEqual(room.connections[0][2].rtcConfig, { iceServers: [] });
  room.emit(RoomEvent.TrackPublished, publication(Track.Source.ScreenShareAudio), { identity: 'bob' });
  room.emit(RoomEvent.TrackPublished, publication(Track.Source.Camera), { identity: 'bob' });
  assert.deepEqual(subscriptions, [
    { source: Track.Source.ScreenShare, value: true },
    { source: Track.Source.ScreenShareAudio, value: true },
  ]);
});

test('a stale capture completion does not overwrite a newer share after stop', async () => {
  const { room, controller } = setup();
  await controller.join('alice');
  const oldCapture = deferred();
  const oldSharing = controller.shareWith(() => oldCapture.promise);
  await controller.stopShare();
  const currentTracks = tracks();
  await controller.shareWith(async () => ({ tracks: currentTracks }));
  const staleTracks = tracks();
  oldCapture.resolve({ tracks: staleTracks });
  await oldSharing;
  assert.equal(controller.getSnapshot().sharing, true);
  assert.deepEqual(currentTracks.map((track) => track.stops), [0, 0]);
  assert.deepEqual(staleTracks.map((track) => track.stops), [1, 1]);
  assert.equal(room.published.length, 2);
  await controller.leave();
});

test('capture constraints failure stops all tracks without publishing any', async () => {
  const { room, controller } = setup();
  await controller.join('alice');
  const captured = tracks();
  captured[0].mediaStreamTrack.applyConstraints = async () => { throw new Error('unsupported'); };
  await controller.shareWith(async () => ({ tracks: captured }));
  assert.equal(room.published.length, 0);
  assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
  assert.equal(controller.getSnapshot().pendingShare, false);
});

test('native reception subscribes only screen video and never renders received audio', async () => {
  const { room, controller } = setup(undefined, { subscribeAudio: false, allowServerIce: true });
  const subscriptions = [];
  const screen = { source: Track.Source.ScreenShare, setSubscribed: () => subscriptions.push('video') };
  const audio = { source: Track.Source.ScreenShareAudio, setSubscribed: () => subscriptions.push('audio') };
  const bob = { identity: 'screen-bob', name: 'Bob', trackPublications: new Map([['v', screen], ['a', audio]]) };
  room.remoteParticipants.set(bob.identity, bob);
  await controller.join('alice');
  room.emit(RoomEvent.TrackPublished, audio, bob);
  room.emit(RoomEvent.TrackSubscribed, tracks()[1], { ...audio, trackSid: 'audio' }, bob);
  room.emit(RoomEvent.TrackSubscribed, tracks()[0], { ...screen, trackSid: 'video' }, bob);
  room.emit(RoomEvent.AudioPlaybackStatusChanged, false);
  assert.deepEqual(subscriptions, ['video']);
  // Even an authenticated native session remains fully local on loopback.
  assert.deepEqual(room.connections[0][2], { autoSubscribe: false, rtcConfig: { iceServers: [] } });
  assert.deepEqual(controller.getSnapshot().tracks.map((track) => track.kind), ['video']);
  assert.equal(controller.getSnapshot().tracks[0].displayName, 'Bob');
  assert.equal(controller.getSnapshot().warning, '');
  await controller.leave();
});

test('server ICE is accepted only for an explicitly enabled non-loopback endpoint', async () => {
  for (const url of ['wss://gul.example', 'wss://127.voice.example']) {
    const { room, controller } = setup(async () => ({ ...grant, url }), { allowServerIce: true });
    await controller.join('alice');
    assert.deepEqual(room.connections[0][2], { autoSubscribe: false });
    await controller.leave();
  }
  for (const url of ['ws://localhost:7880', 'ws://[::1]:7880']) {
    const local = setup(async () => ({ ...grant, url }), { allowServerIce: true });
    await local.controller.join('alice');
    assert.deepEqual(local.room.connections[0][2].rtcConfig, { iceServers: [] });
    await local.controller.leave();
  }
});

test('native publication keeps screen audio while receiving video only', async () => {
  const { room, controller } = setup(undefined, { subscribeAudio: false });
  await controller.join('alice');
  await controller.shareWith(async () => ({ tracks: tracks() }));
  assert.deepEqual(room.published.map((publication) => publication.options.source), [Track.Source.ScreenShare, Track.Source.ScreenShareAudio]);
  assert.equal(controller.getSnapshot().screenAudio, true);
  await controller.leave();
});

test('full-client reconnect stops capture without restarting it after connection recovers', async () => {
  for (const event of [RoomEvent.Reconnecting, RoomEvent.SignalReconnecting]) {
    const { room, controller } = setup(undefined, { stopSharingOnReconnect: true });
    await controller.join('alice');
    const captured = tracks();
    await controller.shareWith(async () => ({ tracks: captured }));
    room.emit(event);
    await tick();
    assert.equal(controller.getSnapshot().status, 'reconnecting');
    assert.deepEqual(captured.map((track) => track.stops), [1, 1]);
    room.emit(RoomEvent.Reconnected);
    assert.equal(controller.getSnapshot().status, 'connected');
    assert.equal(controller.getSnapshot().sharing, false);
    assert.equal(room.published.length, 2);
    await controller.leave();
  }
});
