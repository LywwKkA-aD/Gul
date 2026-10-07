import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import { attachLinuxScreenAudio, findPrivateAudioDevice } from '../src/renderer/media/linux-screen-audio.ts';
import type { ScreenCapture } from '../src/renderer/media/model.ts';

class Video extends EventEmitter {
  readonly mediaStreamTrack = { readyState: 'live' };
  kind = 'video';
  stops = 0;
  stop() {
    ++this.stops;
    this.mediaStreamTrack.readyState = 'ended';
  }
}
function fixture() {
  const video = new Video();
  const audio = {
    kind: 'audio',
    stops: 0,
    stop() {
      ++this.stops;
    },
  };
  const lease = { leaseId: 'a'.repeat(32), deviceLabel: 'Gul-Screen-Audio-' + 'a'.repeat(32) };
  const released: string[] = [];
  let notify: (leaseId: string) => void = () => {};
  const capture = { tracks: [video] } as unknown as ScreenCapture;
  const dependencies = {
    start: async () => lease,
    stop: async (id: string) => {
      released.push(id);
    },
    findDevice: async (label: string) => {
      assert.equal(label, lease.deviceLabel);
      return 'private-exact-device';
    },
    capture: async (deviceId: string) => {
      assert.equal(deviceId, 'private-exact-device');
      return audio as never;
    },
    onEnded: (listener: (leaseId: string) => void) => {
      notify = listener;
      return () => {
        notify = () => {};
      };
    },
  };
  return { video, audio, lease, released, capture, dependencies, notify: (id = lease.leaseId) => notify(id) };
}

test('Linux uses only the generated exact private device and owns one idempotent cleanup', async () => {
  const f = fixture();
  const captured = await attachLinuxScreenAudio(f.capture, f.dependencies);
  assert.equal(captured.tracks.length, 2);
  assert.equal(captured.tracks[1], f.audio);
  captured.cleanup?.();
  captured.cleanup?.();
  assert.deepEqual(f.released, [f.lease.leaseId]);
  assert.equal(f.audio.stops, 1);
});

test('missing, malformed or failed private audio never falls back to a hardware microphone or total mix', async () => {
  for (const mode of ['device', 'lease', 'capture'] as const) {
    const f = fixture();
    await assert.rejects(
      attachLinuxScreenAudio(f.capture, {
        ...f.dependencies,
        ...(mode === 'device' ? { findDevice: async () => null } : {}),
        ...(mode === 'lease'
          ? { start: async () => ({ ...f.lease, deviceLabel: 'default microphone' }) }
          : {}),
        ...(mode === 'capture'
          ? {
              capture: async () => {
                throw new Error('Permission denied');
              },
            }
          : {}),
      }),
    );
    assert.equal(f.video.stops, 1);
    assert.deepEqual(f.released, [f.lease.leaseId]);
  }
});

test('late audio lease or device capture after the display ends is immediately released', async () => {
  for (const phase of ['lease', 'track'] as const) {
    const f = fixture();
    let resolve!: (value: never) => void;
    const pending = new Promise<never>((yes) => {
      resolve = yes;
    });
    const operation = attachLinuxScreenAudio(f.capture, {
      ...f.dependencies,
      ...(phase === 'lease' ? { start: () => pending } : { capture: () => pending }),
    });
    await new Promise((yes) => setTimeout(yes, 1));
    f.video.emit('ended');
    resolve((phase === 'lease' ? f.lease : f.audio) as never);
    await assert.rejects(operation);
    assert.deepEqual(f.released, [f.lease.leaseId]);
    if (phase === 'track') assert.equal(f.audio.stops, 1);
  }
});

test('unexpected native helper exit ends the display and only matching leases affect it', async () => {
  const f = fixture();
  let ended = 0;
  f.video.on('ended', () => {
    ++ended;
  });
  const captured = await attachLinuxScreenAudio(f.capture, f.dependencies);
  f.notify('b'.repeat(32));
  assert.equal(ended, 0);
  f.notify();
  assert.equal(ended, 1);
  assert.equal(f.video.mediaStreamTrack.readyState, 'ended');
  assert.deepEqual(f.released, [f.lease.leaseId]);
  captured.cleanup?.();
});

test('private device discovery accepts exactly one generated input and never a default or output alias', async () => {
  const label = 'Gul-Screen-Audio-' + 'a'.repeat(32);
  const device = (deviceId: string, kind: MediaDeviceKind = 'audioinput', name = label) =>
    ({ deviceId, kind, label: name }) as MediaDeviceInfo;
  for (const [devices, expected] of [
    [[device('exact')], 'exact'],
    [
      [device('default'), device('communications'), device('speaker', 'audiooutput'), device('exact')],
      'exact',
    ],
    [[device('first'), device('second')], null],
    [[device('default'), device('communications')], null],
    [[device('microphone', 'audioinput', 'Physical microphone')], null],
  ] as const) {
    assert.equal(
      await findPrivateAudioDevice(label, { enumerateDevices: async () => [...devices] }, 5),
      expected,
    );
  }
});

test('a hung device enumeration cannot retain an audio lease past the discovery deadline', async () => {
  let resolve!: (devices: MediaDeviceInfo[]) => void;
  const pending = new Promise<MediaDeviceInfo[]>((yes) => {
    resolve = yes;
  });
  const result = findPrivateAudioDevice(
    'Gul-Screen-Audio-' + 'a'.repeat(32),
    {
      enumerateDevices: () => pending,
    },
    5,
  );
  const outcome = await Promise.race([
    result,
    new Promise<string>((yes) => setTimeout(() => yes('hung'), 40)),
  ]);
  resolve([]);
  assert.equal(outcome, null);
});
