import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { Track, RoomEvent, type Room } from 'livekit-client';
import { MediaController } from '../src/renderer/media/controller.ts';
import { screenCaptureOptions } from '../src/renderer/media/capture.ts';
import {
  defaultScreenQuality,
  parseScreenQuality,
  screenPreset,
  screenPresets,
  screenPublishOptions,
  type ScreenQuality,
} from '../src/renderer/media/screen-settings.ts';
import type { Dependencies, ScreenCapture } from '../src/renderer/media/model.ts';
import type { MediaGrant, MediaSession } from '../src/shared/contracts.ts';

const expected = [
  { id: '720p30', width: 1280, height: 720, frameRate: 30, maxBitrate: 2_000_000 },
  { id: '720p60', width: 1280, height: 720, frameRate: 60, maxBitrate: 4_000_000 },
  { id: '1080p30', width: 1920, height: 1080, frameRate: 30, maxBitrate: 5_000_000 },
  { id: '1080p60', width: 1920, height: 1080, frameRate: 60, maxBitrate: 8_000_000 },
] as const;
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
const screenGrant = { ...grant, identity: 'screen.7' };
const session: MediaSession = {
  epoch: 1,
  sessionId: 7,
  identity: 'voice.7',
  name: 'Alice',
  channelId: 0,
  revision: 1,
  grant,
};
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
class TrackFixture extends EventEmitter {
  readonly kind: Track.Kind;
  stopped = false;
  readonly constraints: unknown[] = [];
  readonly mediaStreamTrack = {
    enabled: true,
    readyState: 'live',
    applyConstraints: async (value: unknown) => {
      this.constraints.push(value);
    },
  };
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
  readonly published: { track: unknown; options: ReturnType<typeof screenPublishOptions> }[] = [];
  readonly localParticipant = {
    publishTrack: async (track: unknown, options: ReturnType<typeof screenPublishOptions>) => {
      this.published.push({ track, options });
      return { trackSid: `fixture-${this.published.length}` };
    },
    unpublishTrack: async () => {},
  };
  async connect() {}
  async disconnect() {}
  async startAudio() {}
  async switchActiveDevice() {
    return true;
  }
}
function capture() {
  const video = new TrackFixture(Track.Kind.Video);
  const audio = new TrackFixture(Track.Kind.Audio);
  return { video, audio, result: { tracks: [video, audio] } as unknown as ScreenCapture };
}
function fixture(extra: Partial<Dependencies> = {}) {
  const rooms: RoomFixture[] = [];
  const captured = capture();
  const selections: unknown[][] = [];
  const controller = new MediaController({
    screenGrant: async () => screenGrant,
    audioState: async (state) => state,
    roomFactory: () => {
      const room = new RoomFixture();
      rooms.push(room);
      return room as unknown as Room;
    },
    micFactory: async () => new TrackFixture(Track.Kind.Audio) as any,
    captureFactory: async (...args) => {
      selections.push(args);
      return captured.result;
    },
    audioElementFactory: () => ({ muted: false, remove() {}, play: async () => {} }) as any,
    ...extra,
  });
  return { controller, rooms, selections, ...captured };
}

test('screen qualities are an immutable allowlist with a safe 720p30 default', () => {
  assert.equal(defaultScreenQuality, '720p30');
  assert.deepEqual(
    screenPresets.map(({ label: _label, ...preset }) => preset),
    expected,
  );
  assert.ok(Object.isFrozen(screenPresets));
  for (const preset of expected) {
    assert.equal(parseScreenQuality(preset.id), preset.id);
    assert.equal(
      screenPreset(preset.id),
      screenPresets.find((value) => value.id === preset.id),
    );
    assert.ok(Object.isFrozen(screenPreset(preset.id)));
    assert.ok(screenPreset(preset.id).label.length > 0);
  }
  for (const value of [undefined, null, false, 60, '', '4k60', '720P30', '720p30 ', {}, ['1080p60']])
    assert.equal(parseScreenQuality(value), '720p30');
});

test('all capture presets keep screen stereo audio separate from voice filtering and codec choice', () => {
  const audio = {
    channelCount: 2,
    sampleRate: 48000,
    echoCancellation: false,
    noiseSuppression: false,
    autoGainControl: false,
    restrictOwnAudio: true,
  };
  const audioPublication = screenPublishOptions(false, false, defaultScreenQuality);
  for (const preset of expected) {
    const options = screenCaptureOptions(true, preset.id);
    assert.deepEqual(options.resolution, {
      width: preset.width,
      height: preset.height,
      frameRate: preset.frameRate,
    });
    assert.deepEqual(options.audio, audio);
    assert.equal(options.systemAudio, 'include');
    assert.equal(options.selfBrowserSurface, 'exclude');
    assert.equal(screenCaptureOptions(false, preset.id).audio, false);
    assert.equal(screenCaptureOptions(false, preset.id).systemAudio, 'exclude');
    for (const h264 of [false, true]) {
      const video = screenPublishOptions(true, h264, preset.id);
      assert.equal(video.videoCodec, h264 ? 'h264' : 'vp8');
      assert.equal(video.simulcast, false);
      assert.deepEqual(video.screenShareEncoding, {
        maxBitrate: preset.maxBitrate,
        maxFramerate: preset.frameRate,
      });
      assert.equal(video.degradationPreference, 'maintain-framerate');
      assert.deepEqual(screenPublishOptions(false, h264, preset.id), audioPublication);
    }
  }
  assert.equal(audioPublication.forceStereo, true);
  assert.equal(audioPublication.audioPreset?.maxBitrate, 128_000);
  assert.equal(audioPublication.dtx, false);
  assert.equal(audioPublication.red, true);
});

for (const preset of expected)
  test(`${preset.id} reaches capture, max constraints and publisher encoding without losing the original gesture`, async () => {
    const f = fixture();
    await f.controller.join(session);
    const pendingGrant = deferred<MediaGrant>();
    const start = f.controller.startScreen(pendingGrant.promise, true, preset.id);
    assert.deepEqual(
      f.selections,
      [[true, preset.id]],
      'capture begins synchronously before grant settlement',
    );
    assert.equal(f.controller.getSnapshot().screenQuality, null);
    pendingGrant.resolve(screenGrant);
    await start;
    assert.deepEqual(f.video.constraints, [
      { width: { max: preset.width }, height: { max: preset.height }, frameRate: { max: preset.frameRate } },
    ]);
    assert.deepEqual(f.rooms[1].published[0].options.screenShareEncoding, {
      maxBitrate: preset.maxBitrate,
      maxFramerate: preset.frameRate,
    });
    assert.equal(f.controller.getSnapshot().screenQuality, preset.id);
    assert.equal(f.controller.getSnapshot().screenAudio, 'capturing');
    const another: ScreenQuality = preset.id === '1080p60' ? '720p30' : '1080p60';
    await f.controller.startScreen(screenGrant, true, another);
    assert.equal(f.selections.length, 1, 'the active preset cannot change until stopped');
    assert.equal(f.controller.getSnapshot().screenQuality, preset.id);
    const stopping = f.controller.stopScreen();
    assert.equal(f.controller.getSnapshot().screenQuality, null);
    await stopping;
    await f.controller.leave();
    assert.equal(f.controller.getSnapshot().screenQuality, null);
  });

test('legacy callers default to 720p30; invalid runtime qualities cannot change encoding limits', async () => {
  for (const quality of [undefined, '4k120'] as const) {
    const f = fixture();
    await f.controller.join(session);
    await f.controller.startScreen(screenGrant, true, quality as ScreenQuality);
    assert.deepEqual(f.selections, [[true, '720p30']]);
    assert.equal(f.controller.getSnapshot().screenQuality, '720p30');
    assert.equal(f.rooms[1].published[0].options.screenShareEncoding?.maxBitrate, 2_000_000);
    await f.controller.leave();
  }
});

test('capture or encoder setup failure cannot leave an active quality, and reconnect clears it', async () => {
  for (const extra of [
    {
      captureFactory: async () => {
        throw Error('private fixture');
      },
    },
    {
      preferH264: async () => {
        throw Error('private fixture');
      },
    },
  ]) {
    const f = fixture(extra);
    await f.controller.join(session);
    await f.controller.startScreen(screenGrant, true, '1080p60');
    assert.equal(f.controller.getSnapshot().screenQuality, null);
    assert.equal(f.controller.getSnapshot().sharing, false);
    assert.equal(f.controller.getSnapshot().error.includes('private'), false);
    await f.controller.leave();
  }
  const f = fixture();
  await f.controller.join(session);
  await f.controller.startScreen(screenGrant, true, '1080p30');
  f.rooms[0].emit(RoomEvent.Reconnecting);
  assert.equal(f.controller.getSnapshot().screenQuality, null);
  await f.controller.leave();
});

test('a late selection from the old channel cannot overwrite the new active quality', async () => {
  const pending = deferred<ScreenCapture>();
  const old = capture();
  const next = capture();
  let calls = 0;
  const f = fixture({
    captureFactory: () => (++calls === 1 ? pending.promise : Promise.resolve(next.result)),
  });
  await f.controller.join(session);
  const stale = f.controller.startScreen(screenGrant, true, '720p60');
  await f.controller.leave();
  await f.controller.join({ ...session, epoch: 2 });
  await f.controller.startScreen(screenGrant, true, '1080p60');
  pending.resolve(old.result);
  await stale;
  assert.equal(old.video.stopped, true);
  assert.equal(old.audio.stopped, true);
  assert.equal(f.controller.getSnapshot().screenQuality, '1080p60');
  assert.equal(f.controller.getSnapshot().sharing, true);
  await f.controller.leave();
});
