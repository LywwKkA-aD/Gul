import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { RoomEvent, Track, type Room } from 'livekit-client';
import { MediaController } from '../src/renderer/media/controller.ts';
import type { MediaGrant, MediaSession } from '../src/shared/contracts.ts';

const grant: MediaGrant = {
  url: 'ws://127.0.0.1:5000/capability/rtc',
  token: 'short-lived',
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
  grant,
};
const screenGrant = { ...grant, identity: 'screen.7' };
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
class FakeTrack extends EventEmitter {
  stopped = false;
  muted = false;
  readonly mediaStreamTrack = {
    enabled: true,
    readyState: 'live',
    applyConstraints: async (_value: unknown) => {},
  };
  attached: unknown[] = [];
  readonly kind: Track.Kind;
  constructor(kind: Track.Kind) {
    super();
    this.kind = kind;
  }
  stop() {
    this.stopped = true;
  }
  async mute() {
    this.muted = true;
    this.mediaStreamTrack.enabled = false;
  }
  async unmute() {
    this.muted = false;
    this.mediaStreamTrack.enabled = true;
  }
  attach(element: unknown) {
    this.attached.push(element);
    return element;
  }
  detach(element?: unknown) {
    this.attached = this.attached.filter((item) => item !== element);
    return [];
  }
  volume = 1;
  setVolume(value: number) {
    this.volume = value;
  }
}
class FakeRoom extends EventEmitter {
  readonly options = { webAudioMix: true };
  remoteParticipants = new Map();
  connects: unknown[][] = [];
  disconnected = false;
  published: { track: FakeTrack; options: any }[] = [];
  subscriptions: { source: string; value: boolean }[] = [];
  devices: { kind: string; id: string }[] = [];
  connectGate?: Promise<void>;
  publishGate?: Promise<void>;
  localParticipant = {
    publishTrack: async (track: any, options: any) => {
      this.published.push({ track, options });
      await this.publishGate;
      return { trackSid: `local-${this.published.length}` };
    },
    unpublishTrack: async (track: any) => {
      this.published = this.published.filter((entry) => entry.track !== track);
    },
    publishData: async (_data: Uint8Array, _options: unknown) => {},
  };
  async connect(...args: unknown[]) {
    this.connects.push(args);
    await this.connectGate;
  }
  async disconnect() {
    this.disconnected = true;
  }
  async startAudio() {}
  async switchActiveDevice(kind: string, id: string): Promise<boolean> {
    this.devices.push({ kind, id });
    return true;
  }
}
function harness(extra: Record<string, unknown> = {}) {
  const rooms: FakeRoom[] = [];
  const mic = new FakeTrack(Track.Kind.Audio);
  const controller = new MediaController({
    screenGrant: async () => screenGrant,
    audioState: async (state) => state,
    roomFactory: () => {
      const room = new FakeRoom();
      rooms.push(room);
      return room as unknown as Room;
    },
    micFactory: async () => mic as any,
    captureFactory: async () => ({ tracks: [new FakeTrack(Track.Kind.Video)] as any }),
    audioElementFactory: () => ({ muted: false, remove() {}, play: async () => {} }) as any,
    ...extra,
  });
  return { controller, rooms, mic };
}
function publication(room: FakeRoom, source: Track.Source, id: string) {
  return {
    source,
    trackSid: id,
    isMuted: false,
    setSubscribed: (value: boolean) => room.subscriptions.push({ source, value }),
  };
}

test('voice and viewing work without any screen capture API', async () => {
  const { controller, rooms } = harness({
    captureFactory: async () => {
      throw new Error('Unavailable');
    },
  });
  await controller.join(session);
  assert.equal(controller.getSnapshot().state, 'connected');
  assert.equal(rooms[0].connects[0][2] && (rooms[0].connects[0][2] as any).autoSubscribe, false);
  assert.equal((rooms[0].connects[0][2] as any).rtcConfig.iceTransportPolicy, 'relay');
  const participant = {
    identity: 'screen.8',
    name: 'Bob',
    trackPublications: new Map([['video', publication(rooms[0], Track.Source.ScreenShare, 'video-8')]]),
  };
  rooms[0].remoteParticipants.set(participant.identity, participant);
  rooms[0].emit(RoomEvent.ParticipantConnected, participant);
  assert.equal(controller.getSnapshot().screens[0].identity, 'screen.8');
  assert.equal(rooms[0].subscriptions.length, 0);
  await controller.watchScreen('screen.8');
  const remote = publication(rooms[1], Track.Source.ScreenShare, 'video-8');
  rooms[1].emit(RoomEvent.TrackPublished, remote, participant);
  assert.deepEqual(rooms[1].subscriptions.at(-1), { source: Track.Source.ScreenShare, value: true });
  await controller.leave();
});

test('only voice-owner microphone tracks and selected screen media subscribe', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  const voice = { identity: 'voice.8', name: 'Bob', trackPublications: new Map() };
  const screen = { identity: 'screen.8', name: 'Bob', trackPublications: new Map() };
  rooms[0].emit(RoomEvent.TrackPublished, publication(rooms[0], Track.Source.Microphone, 'mic'), voice);
  rooms[0].emit(
    RoomEvent.TrackPublished,
    publication(rooms[0], Track.Source.ScreenShareAudio, 'screen-audio'),
    screen,
  );
  rooms[0].emit(RoomEvent.TrackPublished, publication(rooms[0], Track.Source.Microphone, 'own'), {
    ...voice,
    identity: 'voice.7',
  });
  assert.deepEqual(rooms[0].subscriptions, [{ source: Track.Source.Microphone, value: true }]);
  await controller.watchScreen('screen.8');
  rooms[1].emit(
    RoomEvent.TrackPublished,
    publication(rooms[1], Track.Source.ScreenShareAudio, 'audio'),
    screen,
  );
  rooms[1].emit(RoomEvent.TrackPublished, publication(rooms[1], Track.Source.ScreenShare, 'other'), {
    ...screen,
    identity: 'screen.9',
  });
  assert.deepEqual(rooms[1].subscriptions, [
    { source: Track.Source.ScreenShareAudio, value: true },
    { source: Track.Source.ScreenShare, value: false },
  ]);
  await controller.watchScreen(null);
  assert.equal(rooms[1].disconnected, true);
  await controller.leave();
});

test('late microphone capture after channel leave is stopped and never published', async () => {
  const pending = deferred<any>();
  const { controller, rooms } = harness({ micFactory: () => pending.promise });
  const joining = controller.join(session);
  await new Promise((resolve) => setImmediate(resolve));
  await controller.leave();
  const late = new FakeTrack(Track.Kind.Audio);
  pending.resolve(late);
  await joining;
  assert.equal(late.stopped, true);
  assert.equal(rooms[0].published.length, 0);
  assert.equal(controller.getSnapshot().state, 'disconnected');
});

test('late screen selection after channel leave cannot publish', async () => {
  const pending = deferred<any>();
  const { controller, rooms } = harness({ captureFactory: () => pending.promise });
  await controller.join(session);
  const sharing = controller.startScreen(screenGrant, true);
  await controller.leave();
  const late = new FakeTrack(Track.Kind.Video);
  pending.resolve({ tracks: [late] });
  await sharing;
  assert.equal(late.stopped, true);
  assert.equal(rooms.length, 1);
  assert.equal(controller.getSnapshot().sharing, false);
});

test('screen capture publishes bounded single video encoding and stereo audio separately', async () => {
  const video = new FakeTrack(Track.Kind.Video);
  const audio = new FakeTrack(Track.Kind.Audio);
  const { controller, rooms } = harness({ captureFactory: async () => ({ tracks: [video, audio] }) });
  await controller.join(session);
  await controller.startScreen(screenGrant, true);
  assert.equal(controller.getSnapshot().sharing, true);
  assert.equal(rooms[1].published[0].options.simulcast, false);
  assert.equal(rooms[1].published[0].options.screenShareEncoding.maxFramerate, 30);
  assert.ok(rooms[1].published[0].options.screenShareEncoding.maxBitrate <= 2_500_000);
  assert.equal(rooms[1].published[1].options.source, Track.Source.ScreenShareAudio);
  assert.equal(rooms[1].published[1].options.forceStereo, true);
  assert.equal(rooms[1].published[1].options.audioPreset.maxBitrate, 128_000);
  assert.equal(audio.attached.length, 0);
  rooms[0].emit(RoomEvent.Reconnecting);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(video.stopped, true);
  assert.equal(audio.stopped, true);
  assert.equal(controller.getSnapshot().sharing, false);
  await controller.leave();
});

test('capture denial preserves voice and reports a safe message', async () => {
  const { controller } = harness({
    captureFactory: async () => {
      throw new Error('token=secret example');
    },
  });
  await controller.join(session);
  await controller.startScreen(screenGrant, true);
  assert.equal(controller.getSnapshot().state, 'connected');
  assert.ok(controller.getSnapshot().error.length > 0);
  assert.equal(controller.getSnapshot().error.includes('secret'), false);
  await controller.leave();
});

test('microphone permission denial still permits receiving voice and watching', async () => {
  const { controller } = harness({
    micFactory: async () => {
      throw new Error('permission denied');
    },
  });
  await controller.join(session);
  assert.equal(controller.getSnapshot().state, 'connected');
  assert.equal(controller.getSnapshot().muted, true);
  assert.ok(controller.getSnapshot().warning.length > 0);
  await controller.watchScreen('screen.8');
  await controller.leave();
});

test('chat validates source, limits, room topic and displays literal text', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  const data = new TextEncoder().encode(JSON.stringify({ text: '<b>hello</b>' }));
  const from = { identity: 'voice.8', name: 'Bob' };
  rooms[0].emit(RoomEvent.DataReceived, data, from, 0, 'gul.chat.v1');
  rooms[0].emit(RoomEvent.DataReceived, data, { ...from, identity: 'screen.8' }, 0, 'gul.chat.v1');
  rooms[0].emit(RoomEvent.DataReceived, data, from, 0, 'wrong');
  rooms[0].emit(RoomEvent.DataReceived, new TextEncoder().encode('{broken'), from, 0, 'gul.chat.v1');
  assert.equal(controller.getSnapshot().chat.length, 1);
  assert.equal(controller.getSnapshot().chat[0].text, '<b>hello</b>');
  await controller.sendChat(' local ');
  assert.equal(controller.getSnapshot().chat[1].text, 'local');
  await assert.rejects(controller.sendChat('x'.repeat(5001)));
  await controller.leave();
});

test('deafen silences received audio without subscribing own audio twice', async () => {
  const { controller, rooms, mic } = harness();
  await controller.join(session);
  const track = new FakeTrack(Track.Kind.Audio);
  const pub = publication(rooms[0], Track.Source.Microphone, 'voice-8');
  rooms[0].emit(RoomEvent.TrackSubscribed, track, pub, { identity: 'voice.8', name: 'Bob' });
  assert.equal(track.attached.length, 1);
  await controller.setAudio({ muted: false, deafened: true });
  assert.equal((track.attached[0] as any).muted, true);
  assert.equal(track.volume, 0);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  await controller.setAudio({ muted: false, deafened: false });
  assert.equal((track.attached[0] as any).muted, true);
  assert.equal(track.volume, 1);
  assert.equal(mic.mediaStreamTrack.enabled, true);
  await controller.leave();
});

test('mute preferences survive changing channels and prevent publishing an enabled microphone', async () => {
  const { controller, rooms, mic } = harness();
  await controller.join(session);
  await controller.setAudio({ muted: true, deafened: false });
  await controller.join({
    ...session,
    epoch: 2,
    channelId: 1,
    grant: { ...grant, channelId: 1, room: 'gul.1' },
  });
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  assert.equal(rooms[0].disconnected, true);
  await controller.leave();
});

test('PTT mute and release apply before broker reconciliation and stale confirmations cannot reopen mic', async () => {
  const pending = deferred<any>();
  const { controller, mic } = harness({ audioState: () => pending.promise });
  await controller.join(session);
  const muting = controller.setAudio({ muted: true, deafened: false });
  assert.equal(mic.mediaStreamTrack.enabled, false);
  const opening = controller.setAudio({ muted: false, deafened: false });
  assert.equal(mic.mediaStreamTrack.enabled, true);
  const final = controller.setAudio({ muted: true, deafened: false });
  assert.equal(mic.mediaStreamTrack.enabled, false);
  pending.resolve({ muted: true, deafened: false });
  await Promise.all([muting, opening, final]);
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  await controller.leave();
});

test('latest channel join wins when old teardown resolves last', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  const gate = deferred<void>();
  rooms[0].disconnect = () => gate.promise;
  const older = controller.join({ ...session, epoch: 2 });
  const latest = controller.join({
    ...session,
    epoch: 3,
    channelId: 1,
    grant: { ...grant, channelId: 1, room: 'gul.1' },
  });
  await latest;
  gate.resolve();
  await older;
  assert.equal(rooms.length, 2);
  assert.equal(rooms[1].connects[0][1], grant.token);
  assert.equal(controller.getSnapshot().state, 'connected');
  await controller.leave();
});

test('stale selected-screen grant cannot connect after a newer selection', async () => {
  const pending = deferred<MediaGrant>();
  let calls = 0;
  const { controller, rooms } = harness({
    screenGrant: () => (++calls === 1 ? pending.promise : Promise.resolve(screenGrant)),
  });
  await controller.join(session);
  const older = controller.watchScreen('screen.8');
  await controller.watchScreen('screen.9');
  pending.resolve(screenGrant);
  await older;
  const pub = publication(rooms[1], Track.Source.ScreenShare, 'video-8');
  rooms[1].emit(RoomEvent.TrackPublished, pub, { identity: 'screen.8' });
  rooms[1].emit(RoomEvent.TrackPublished, { ...pub, trackSid: 'video-9' }, { identity: 'screen.9' });
  assert.deepEqual(
    rooms[1].subscriptions.map((entry) => entry.value),
    [false, true],
  );
  await controller.leave();
});

test('screen participant leaving does not detach the same owners voice audio', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  const track = new FakeTrack(Track.Kind.Audio);
  rooms[0].emit(RoomEvent.TrackSubscribed, track, publication(rooms[0], Track.Source.Microphone, 'mic-8'), {
    identity: 'voice.8',
  });
  rooms[0].emit(RoomEvent.ParticipantDisconnected, { identity: 'screen.8' });
  assert.equal(track.attached.length, 1);
  rooms[0].emit(RoomEvent.ParticipantDisconnected, { identity: 'voice.8' });
  assert.equal(track.attached.length, 0);
  await controller.leave();
});

test('unpublished selected video stops its screen audio too', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  await controller.watchScreen('screen.8');
  const track = new FakeTrack(Track.Kind.Audio);
  const p = { identity: 'screen.8' };
  rooms[1].emit(
    RoomEvent.TrackSubscribed,
    track,
    publication(rooms[1], Track.Source.ScreenShareAudio, 'audio-8'),
    p,
  );
  assert.equal(track.attached.length, 1);
  rooms[0].emit(RoomEvent.TrackUnpublished, publication(rooms[0], Track.Source.ScreenShare, 'video-8'), p);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(track.attached.length, 0);
  assert.equal(rooms[1].disconnected, true);
  await controller.leave();
});

test('voice and demonstration volume are independent even for the same participant', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  await controller.watchScreen('screen.8');
  const mic = new FakeTrack(Track.Kind.Audio);
  const screen = new FakeTrack(Track.Kind.Audio);
  rooms[0].emit(RoomEvent.TrackSubscribed, mic, publication(rooms[0], Track.Source.Microphone, 'mic-8'), {
    identity: 'voice.8',
  });
  rooms[1].emit(
    RoomEvent.TrackSubscribed,
    screen,
    publication(rooms[1], Track.Source.ScreenShareAudio, 'audio-8'),
    { identity: 'screen.8' },
  );
  controller.setUserVolume('screen.8', 0.25);
  assert.equal(screen.volume, 0.25);
  assert.equal(mic.volume, 1);
  controller.setUserVolume('voice.8', 0.75);
  assert.equal(screen.volume, 0.25);
  assert.equal(mic.volume, 0.75);
  controller.setUserVolume('screen.8', NaN);
  assert.equal(screen.volume, 0.25);
  await controller.leave();
});

test('participant mute retains 0–200% gain and remains independent of screen mute and deafen', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  await controller.watchScreen('screen.8');
  const voice = new FakeTrack(Track.Kind.Audio),
    screen = new FakeTrack(Track.Kind.Audio);
  rooms[0].emit(RoomEvent.TrackSubscribed, voice, publication(rooms[0], Track.Source.Microphone, 'mic-8'), {
    identity: 'voice.8',
  });
  rooms[1].emit(
    RoomEvent.TrackSubscribed,
    screen,
    publication(rooms[1], Track.Source.ScreenShareAudio, 'screen-8'),
    { identity: 'screen.8' },
  );
  controller.setUserVolume('voice.8', 1.6);
  controller.setUserVolume('screen.8', 0.4);
  assert.equal(voice.volume, 1.6);
  assert.equal(screen.volume, 0.4);
  controller.setUserMuted('voice.8', true);
  assert.equal(voice.volume, 0);
  assert.equal(screen.volume, 0.4);
  controller.setUserVolume('voice.8', 1.8);
  assert.equal(voice.volume, 0);
  controller.setUserMuted('voice.8', false);
  assert.equal(voice.volume, 1.8);
  controller.setUserMuted('screen.8', true);
  assert.equal(screen.volume, 0);
  assert.equal(voice.volume, 1.8);
  await controller.setAudio({ muted: false, deafened: true });
  assert.equal(voice.volume, 0);
  assert.equal(screen.volume, 0);
  await controller.setAudio({ muted: false, deafened: false });
  assert.equal(voice.volume, 1.8);
  assert.equal(screen.volume, 0);
  controller.setUserMuted('screen.8', false);
  assert.equal(screen.volume, 0.4);
  controller.setUserVolume('voice.8', 3);
  assert.equal(voice.volume, 2);
  controller.setUserVolume('voice.8', -1);
  assert.equal(voice.volume, 0);
  controller.setUserVolume('voice.8', Number.NaN);
  assert.equal(voice.volume, 0);
  controller.setUserVolume('unknown', 2);
  controller.setUserMuted('unknown', true);
  assert.equal(screen.volume, 0.4);
  await controller.leave();
});

test('WebAudio playback never unmutes its attached HTML audio element', async () => {
  const mutedWrites: boolean[] = [];
  const { controller, rooms } = harness({
    audioElementFactory: () => {
      let muted = false;
      return {
        get muted() {
          return muted;
        },
        set muted(value: boolean) {
          muted = value;
          mutedWrites.push(value);
        },
        volume: 1,
        remove() {},
        play: async () => {},
      };
    },
  });
  await controller.join(session);
  const voice = new FakeTrack(Track.Kind.Audio);
  rooms[0].emit(RoomEvent.TrackSubscribed, voice, publication(rooms[0], Track.Source.Microphone, 'mic-8'), {
    identity: 'voice.8',
  });
  controller.setUserVolume('voice.8', 1.5);
  await controller.setAudio({ muted: false, deafened: true });
  await controller.setAudio({ muted: false, deafened: false });
  assert.equal(mutedWrites.every(Boolean), true);
  assert.equal((voice.attached[0] as HTMLAudioElement).volume, 0);
  assert.equal(voice.volume, 1.5);
  await controller.leave();
});

test('output device switching is delegated to both SDK rooms and inherited by a later screen room', async () => {
  const { controller, rooms } = harness();
  await controller.join(session);
  await controller.setDevice('audiooutput', 'headphones');
  assert.deepEqual(rooms[0].devices, [{ kind: 'audiooutput', id: 'headphones' }]);
  await controller.watchScreen('screen.8');
  assert.deepEqual(rooms[1].devices, [{ kind: 'audiooutput', id: 'headphones' }]);
  await controller.setDevice('audiooutput', 'speakers');
  assert.deepEqual(
    rooms.map((room) => room.devices.at(-1)),
    [
      { kind: 'audiooutput', id: 'speakers' },
      { kind: 'audiooutput', id: 'speakers' },
    ],
  );
  await controller.setDevice('audioinput', 'microphone');
  assert.deepEqual(rooms[0].devices.at(-1), { kind: 'audioinput', id: 'microphone' });
  assert.deepEqual(rooms[1].devices.at(-1), { kind: 'audiooutput', id: 'speakers' });
  await controller.leave();
});

test('device preferences can be selected before join without opening capture and apply to the first microphone', async () => {
  const captures: (string | undefined)[] = [];
  const mic = new FakeTrack(Track.Kind.Audio);
  const { controller, rooms } = harness({
    micFactory: async (id?: string) => {
      captures.push(id);
      return mic;
    },
  });
  await controller.setDevice('audioinput', 'preferred-microphone');
  await controller.setDevice('audiooutput', 'preferred-headphones');
  assert.equal(rooms.length, 0);
  assert.deepEqual(captures, []);
  await controller.join(session);
  assert.deepEqual(captures, ['preferred-microphone']);
  assert.deepEqual(rooms[0].devices, [{ kind: 'audiooutput', id: 'preferred-headphones' }]);
  await controller.join({ ...session, epoch: 2 });
  assert.deepEqual(captures, ['preferred-microphone', 'preferred-microphone']);
  assert.deepEqual(rooms[1].devices, [{ kind: 'audiooutput', id: 'preferred-headphones' }]);
  await controller.leave();
});

test('failed device selection rejects with a sanitized message so UI cannot persist it', async () => {
  const captures: (string | undefined)[] = [];
  const { controller, rooms } = harness({
    micFactory: async (id?: string) => {
      captures.push(id);
      return new FakeTrack(Track.Kind.Audio);
    },
  });
  await controller.setDevice('audioinput', 'preferred-microphone');
  await controller.join(session);
  rooms[0].switchActiveDevice = async () => {
    throw new Error('private device token=secret');
  };
  await assert.rejects(
    controller.setDevice('audioinput', 'rejected-microphone'),
    /Не удалось выбрать аудиоустройство/,
  );
  assert.equal(controller.getSnapshot().error.includes('secret'), false);
  await controller.join({ ...session, epoch: 2 });
  assert.deepEqual(captures, ['preferred-microphone', 'preferred-microphone']);
  await controller.leave();
});

test('a microphone publish failure cannot leave capture running', async () => {
  const mic = new FakeTrack(Track.Kind.Audio);
  const { controller, rooms } = harness({
    micFactory: async () => mic as any,
    roomFactory: () => {
      const room = new FakeRoom();
      room.localParticipant.publishTrack = async () => {
        throw new Error('secret token');
      };
      rooms.push(room);
      return room as any;
    },
  });
  await controller.join(session);
  assert.equal(mic.stopped, true);
  assert.equal(controller.getSnapshot().state, 'connected');
  assert.equal(controller.getSnapshot().warning.includes('secret'), false);
  await controller.leave();
});

test('capture starts during the user gesture before asynchronous screen grant resolves', async () => {
  const pending = deferred<MediaGrant>();
  let selected = false;
  const video = new FakeTrack(Track.Kind.Video);
  const { controller, rooms } = harness({
    captureFactory: async () => {
      selected = true;
      return { tracks: [video] };
    },
  });
  await controller.join(session);
  const starting = controller.startScreen(pending.promise, false);
  assert.equal(selected, true);
  assert.equal(rooms.length, 1);
  pending.resolve(screenGrant);
  await starting;
  assert.equal(controller.getSnapshot().sharing, true);
  await controller.leave();
});

test('rejected screen grant is handled while picker is pending and selected tracks are released', async () => {
  const pending = deferred<any>();
  const video = new FakeTrack(Track.Kind.Video);
  const { controller, rooms } = harness({ captureFactory: () => pending.promise });
  await controller.join(session);
  const starting = controller.startScreen(Promise.reject(new Error('secret grant failure')), false);
  await new Promise((resolve) => setImmediate(resolve));
  pending.resolve({ tracks: [video] });
  await starting;
  assert.equal(video.stopped, true);
  assert.equal(rooms.length, 1);
  assert.equal(controller.getSnapshot().error.includes('secret'), false);
  assert.equal(controller.getSnapshot().state, 'connected');
  await controller.leave();
});

test('channel change while awaiting grant releases already selected tracks', async () => {
  const pending = deferred<MediaGrant>();
  const video = new FakeTrack(Track.Kind.Video);
  const { controller } = harness({ captureFactory: async () => ({ tracks: [video] }) });
  await controller.join(session);
  const starting = controller.startScreen(pending.promise, false);
  await new Promise((resolve) => setImmediate(resolve));
  await controller.join({
    ...session,
    epoch: 2,
    channelId: 1,
    grant: { ...grant, channelId: 1, room: 'gul.1' },
  });
  assert.equal(video.stopped, true);
  pending.resolve(screenGrant);
  await starting;
  assert.equal(video.stopped, true);
  assert.equal(controller.getSnapshot().sharing, false);
  await controller.leave();
});

test('failed mute metadata cannot reopen the microphone or discard mute across channel change', async () => {
  const { controller, mic } = harness({
    audioState: async () => {
      throw new Error('broker timeout token=secret');
    },
  });
  await controller.join(session);
  await controller.setAudio({ muted: true, deafened: false });
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  assert.equal(controller.getSnapshot().error.includes('secret'), false);
  await controller.join({ ...session, epoch: 2 });
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  await controller.leave();
});

test('failed deafen metadata cannot restart remote playback or the microphone', async () => {
  const { controller, rooms, mic } = harness({
    audioState: async () => {
      throw new Error('broker timeout');
    },
  });
  await controller.join(session);
  const voice = new FakeTrack(Track.Kind.Audio);
  rooms[0].emit(RoomEvent.TrackSubscribed, voice, publication(rooms[0], Track.Source.Microphone, 'mic-8'), {
    identity: 'voice.8',
  });
  await controller.setAudio({ muted: false, deafened: true });
  assert.equal(controller.getSnapshot().deafened, true);
  assert.equal((voice.attached[0] as any).muted, true);
  assert.equal(voice.volume, 0);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  await controller.leave();
});

test('rapid PTT updates keep newest local mute when older unmute metadata succeeds and newest mute fails', async () => {
  const opening = deferred<any>();
  const closing = deferred<any>();
  let calls = 0;
  const { controller, mic } = harness({
    audioState: () => (++calls === 1 ? opening.promise : closing.promise),
  });
  await controller.join(session);
  const oldUpdate = controller.setAudio({ muted: false, deafened: false });
  await new Promise((resolve) => setImmediate(resolve));
  const newUpdate = controller.setAudio({ muted: true, deafened: false });
  assert.equal(mic.mediaStreamTrack.enabled, false);
  opening.resolve({ muted: false, deafened: false });
  await oldUpdate;
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  closing.reject(new Error('broker timeout'));
  await newUpdate;
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(mic.mediaStreamTrack.enabled, false);
  await controller.leave();
});

test('switching microphone cannot reopen a locally muted capture', async () => {
  const { controller, rooms, mic } = harness();
  await controller.join(session);
  await controller.setAudio({ muted: true, deafened: false });
  rooms[0].switchActiveDevice = async () => {
    mic.mediaStreamTrack.enabled = true;
    return true;
  };
  await controller.setDevice('audioinput', 'new-microphone');
  assert.equal(mic.mediaStreamTrack.enabled, false);
  await controller.leave();
});

test('playback receiver statistics update the subscribed snapshot with an actual voice RTT', async () => {
  const { controller, rooms, mic } = harness();
  let notify!: () => void;
  const measured = new Promise<void>((resolve) => {
    notify = resolve;
  });
  const unsubscribe = controller.subscribe(() => {
    if (controller.getSnapshot().pingMs === 52) notify();
  });
  await controller.join(session);
  const voice = new FakeTrack(Track.Kind.Audio);
  (voice as any).receiver = {
    getStats: async () =>
      new Map([
        [
          'pair',
          {
            id: 'pair',
            type: 'candidate-pair',
            state: 'succeeded',
            nominated: true,
            responsesReceived: 2,
            currentRoundTripTime: 0.052,
          },
        ],
      ]),
  };
  (mic as any).sender = {
    getStats: async () => {
      throw new Error('private statistics failure');
    },
  };
  rooms[0].emit(RoomEvent.TrackSubscribed, voice, publication(rooms[0], Track.Source.Microphone, 'voice'), {
    identity: 'voice.8',
  });
  let timeout!: ReturnType<typeof setTimeout>;
  try {
    await Promise.race([
      measured,
      new Promise<void>((_resolve, reject) => {
        timeout = setTimeout(() => reject(new Error('No RTT update')), 2000);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
    unsubscribe();
    await controller.leave();
  }
});
