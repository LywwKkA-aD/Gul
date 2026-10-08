import assert from 'node:assert/strict';
import test from 'node:test';
import { Microphone } from '../src/renderer/media/microphone.ts';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';

function harness(overrides: Record<string, unknown> = {}) {
  const track = {
    mediaStreamTrack: { enabled: true },
    stopped: false,
    constraints: [] as unknown[],
    stop() {
      this.stopped = true;
      this.mediaStreamTrack.enabled = false;
    },
    async mute() {
      this.mediaStreamTrack.enabled = false;
    },
    async unmute() {
      this.mediaStreamTrack.enabled = true;
    },
    async applyConstraints(options: unknown) {
      this.constraints.push(options);
    },
    async restartTrack(options: unknown) {
      this.constraints.push(options);
    },
  };
  const processor = {
    closed: 0,
    settings: [] as unknown[],
    update(value: unknown) {
      this.settings.push(value);
    },
    async destroy() {
      this.closed++;
    },
  };
  const warnings: string[] = [];
  const readings: unknown[] = [];
  const captures: unknown[][] = [];
  const room = {
    localParticipant: {
      publishTrack: async (_track: unknown) => {
        assert.equal(track.mediaStreamTrack.enabled, false);
      },
      unpublishTrack: async () => {},
    },
  };
  const microphone = new Microphone({
    capture: async (...options: unknown[]) => {
      captures.push(options);
      return track as any;
    },
    processor: async () => processor,
    warning: (message: string) => warnings.push(message),
    reading: (value) => readings.push(value),
    ...overrides,
  });
  return { microphone, track, processor, warnings, readings, captures, room };
}
test('raw microphone remains silent until its processor is ready and manual mute overrides processing', async () => {
  let resolve!: (value: any) => void;
  const pending = new Promise<any>((yes) => {
    resolve = yes;
  });
  const { microphone, track, processor, room } = harness({ processor: () => pending });
  const starting = microphone.start(room as any, 'input', () => true);
  await new Promise((yes) => setImmediate(yes));
  assert.equal(track.mediaStreamTrack.enabled, false);
  await microphone.synchronize({ muted: false, deafened: false });
  assert.equal(track.mediaStreamTrack.enabled, false);
  resolve(processor);
  await starting;
  assert.equal(track.mediaStreamTrack.enabled, true);
  microphone.apply({ muted: true, deafened: false });
  assert.equal(track.mediaStreamTrack.enabled, false);
  await microphone.stop();
});
test('channel cancellation closes a late processor and never re-enables captured audio', async () => {
  let resolve!: (value: any) => void;
  const pending = new Promise<any>((yes) => {
    resolve = yes;
  });
  const { microphone, track, processor, room } = harness({ processor: () => pending });
  const starting = microphone.start(room as any, 'input', () => true);
  await new Promise((yes) => setImmediate(yes));
  await microphone.stop();
  resolve(processor);
  await starting;
  assert.equal(track.stopped, true);
  assert.equal(track.mediaStreamTrack.enabled, false);
  assert.equal(processor.closed, 1);
  assert.equal(microphone.captured, false);
});
test('VAD failure is fail-closed, while default voice can use the original Chromium path', async () => {
  const vad = harness({
    processor: async () => {
      throw new Error('no worklet');
    },
  });
  await vad.microphone.configure({ mode: 'vad' });
  await vad.microphone.start(vad.room as any, undefined, () => true);
  assert.equal(vad.track.stopped, true);
  assert.equal(vad.microphone.captured, false);
  assert.equal(vad.warnings.length, 1);
  const basic = harness({ processor: async () => undefined });
  await basic.microphone.start(basic.room as any, undefined, () => true);
  assert.equal(basic.track.mediaStreamTrack.enabled, true);
  await assert.rejects(basic.microphone.configure({ mode: 'vad' }));
  assert.equal(basic.microphone.settings.mode, 'continuous');
  await basic.microphone.stop();
});
test('capture flags are preselected, settings apply once without reopening a locally muted track', async () => {
  const { microphone, captures, processor, track, room } = harness();
  await microphone.configure({ noiseSuppression: false, inputGain: 1.5 });
  await microphone.start(room as any, 'preferred', () => true);
  assert.equal(captures[0][0], 'preferred');
  assert.equal((captures[0][1] as any).inputGain, 1.5);
  microphone.apply({ muted: true, deafened: false });
  await microphone.configure({ echoCancellation: false });
  assert.equal(track.constraints.length, 1);
  assert.equal(track.mediaStreamTrack.enabled, false);
  assert.equal(processor.settings.length, 1);
  assert.equal(microphone.settings.echoCancellation, false);
  assert.equal(defaultVoiceSettings.echoCancellation, true);
  await microphone.stop();
});
test('failed Chromium constraints roll preferences back while keeping manual mute', async () => {
  const { microphone, track, room } = harness();
  await microphone.start(room as any, undefined, () => true);
  microphone.apply({ muted: true, deafened: true });
  track.restartTrack = async () => {
    throw new Error('device is gone');
  };
  await assert.rejects(microphone.configure({ echoCancellation: false }), /настройк/iu);
  assert.equal(microphone.settings.echoCancellation, true);
  assert.equal(track.mediaStreamTrack.enabled, false);
  await microphone.stop();
});
test('processing flags restart capture with mono48k/device constraints instead of ineffective dynamic constraints', async () => {
  const { microphone, track, room } = harness();
  track.applyConstraints = async () => {
    assert.fail('Chromium may resolve dynamic flags without changing its capture processing.');
  };
  await microphone.start(room as any, 'chosen-input', () => true);
  await microphone.configure({ noiseSuppression: false, autoGainControl: false });
  assert.deepEqual(track.constraints, [
    {
      deviceId: { exact: 'chosen-input' },
      channelCount: 1,
      sampleRate: 48000,
      noiseSuppression: false,
      autoGainControl: false,
      echoCancellation: true,
    },
  ]);
  assert.equal(track.mediaStreamTrack.enabled, true);
  await microphone.stop();
});
test('processing restart stays silent through manual/PTT mute and late cancellation', async () => {
  const { microphone, track, room } = harness();
  await microphone.start(room as any, undefined, () => true);
  let finish!: () => void;
  track.restartTrack = async () => {
    assert.equal(track.mediaStreamTrack.enabled, false);
    await new Promise<void>((resolve) => {
      finish = resolve;
    });
    // The capture promise can acquire a replacement after the old raw track stopped.
    track.mediaStreamTrack = { enabled: true };
  };
  const change = microphone.configure({ noiseSuppression: false });
  await new Promise((resolve) => setImmediate(resolve));
  microphone.apply({ muted: false, deafened: false });
  assert.equal(track.mediaStreamTrack.enabled, false);
  microphone.apply({ muted: true, deafened: false });
  finish();
  await change;
  assert.equal(track.mediaStreamTrack.enabled, false);
  await microphone.synchronize({ muted: false, deafened: false });
  assert.equal(track.mediaStreamTrack.enabled, true);
  const cancelled = microphone.configure({ noiseSuppression: true });
  await new Promise((resolve) => setImmediate(resolve));
  await microphone.stop();
  finish();
  await cancelled;
  assert.equal(track.stopped, true);
  assert.equal(track.mediaStreamTrack.enabled, false);
});
test('meter reports apply manual state and late callback cannot update a newer channel', async () => {
  let report!: (reading: { level: number; active: boolean }) => void;
  const processor = { update() {}, async destroy() {} };
  const { microphone, room, readings } = harness({
    processor: async (_track: unknown, _settings: unknown, callback: typeof report) => {
      report = callback;
      return processor;
    },
  });
  await microphone.start(room as any, undefined, () => true);
  report({ level: 0.4, active: true });
  assert.deepEqual(readings.at(-1), { level: 0.4, active: true, available: true });
  microphone.apply({ muted: true, deafened: false });
  report({ level: 0.4, active: true });
  assert.deepEqual(readings.at(-1), { level: 0, active: false, available: true });
  const count = readings.length;
  await microphone.stop();
  report({ level: 1, active: true });
  assert.equal(readings.length, count);
});
test('preferences changed during capture reconcile raw flags before publication and processor unmute', async () => {
  let complete!: (value: any) => void;
  const pending = new Promise<any>((resolve) => {
    complete = resolve;
  });
  const { microphone, track, room } = harness({ capture: () => pending });
  const opening = microphone.start(room as any, undefined, () => true);
  await microphone.configure({ noiseSuppression: false });
  complete(track);
  await opening;
  assert.equal(microphone.settings.noiseSuppression, false);
  assert.equal((track.constraints[0] as any).noiseSuppression, false);
  assert.deepEqual((track.constraints[0] as any).deviceId, { exact: 'default' });
  await microphone.stop();
});
test('device changes during initial capture reconcile before publication and while processing opens', async () => {
  let captured!: (value: any) => void;
  let processing!: (value: any) => void;
  const capture = new Promise<any>((resolve) => {
    captured = resolve;
  });
  const processor = new Promise<any>((resolve) => {
    processing = resolve;
  });
  const fixture = harness({ capture: () => capture, processor: () => processor });
  fixture.room.localParticipant.publishTrack = async () => {
    assert.deepEqual((fixture.track.constraints.at(-1) as any).deviceId, { exact: 'second-input' });
    assert.equal(fixture.track.mediaStreamTrack.enabled, false);
  };
  const opening = fixture.microphone.start(fixture.room as any, 'first-input', () => true);
  fixture.microphone.useDevice('second-input');
  captured(fixture.track);
  await new Promise((resolve) => setImmediate(resolve));
  fixture.microphone.useDevice('third-input');
  processing(fixture.processor);
  await opening;
  assert.equal(fixture.track.constraints.length, 2);
  assert.deepEqual((fixture.track.constraints.at(-1) as any).deviceId, { exact: 'third-input' });
  assert.equal(fixture.track.mediaStreamTrack.enabled, true);
  await fixture.microphone.stop();
});
test('newer gain patch cannot skip restoring a failed raw processing change', async () => {
  const { microphone, track, processor, room } = harness();
  await microphone.start(room as any, undefined, () => true);
  let reject!: (error: Error) => void;
  let first = true;
  track.restartTrack = async (options) => {
    track.constraints.push(options);
    if (first) {
      first = false;
      await new Promise<void>((_, no) => {
        reject = no;
      });
    }
  };
  const changing = microphone.configure({ noiseSuppression: false });
  const failed = assert.rejects(changing, /настройк/iu);
  await new Promise((resolve) => setImmediate(resolve));
  const gain = microphone.configure({ inputGain: 1.5 });
  reject(new Error('capture failed'));
  await failed;
  await gain;
  assert.equal(microphone.settings.noiseSuppression, true);
  assert.equal(microphone.settings.inputGain, 1.5);
  assert.equal((track.constraints.at(-1) as any).noiseSuppression, false);
  assert.equal((processor.settings.at(-1) as any).noiseSuppression, true);
  assert.deepEqual((track.constraints.at(-1) as any).deviceId, { exact: 'default' });
  await microphone.stop();
});
test('fallback cannot unmute VAD chosen during the final raw-capture reconciliation', async () => {
  const fixture = harness({
    processor: async () => {
      await fixture.microphone.configure({ noiseSuppression: false });
      return undefined;
    },
  });
  let finish!: () => void;
  fixture.track.restartTrack = () =>
    new Promise<void>((resolve) => {
      finish = resolve;
    });
  const opening = fixture.microphone.start(fixture.room as any, undefined, () => true);
  await new Promise((resolve) => setImmediate(resolve));
  await fixture.microphone.configure({ mode: 'vad' });
  finish();
  await opening;
  assert.equal(fixture.track.stopped, true);
  assert.equal(fixture.track.mediaStreamTrack.enabled, false);
  assert.equal(fixture.microphone.captured, false);
});
test('SDK processor restart sees the new noise settings before its readiness handshake', async () => {
  const { microphone, track, room, processor } = harness();
  await microphone.start(room as any, undefined, () => true);
  track.restartTrack = async () => {
    assert.equal((processor.settings.at(-1) as any)?.noiseSuppression, false);
    assert.equal(track.mediaStreamTrack.enabled, false);
  };
  await microphone.configure({ noiseSuppression: false });
  await microphone.stop();
});
test('late audio-thread failure closes capture, marks processing unavailable and warns once', async () => {
  let failure!: () => void;
  const fixture = harness({
    processor: async (_track: unknown, _settings: unknown, _reading: unknown, onFailure: () => void) => {
      failure = onFailure;
      return fixture.processor;
    },
  });
  await fixture.microphone.start(fixture.room as any, undefined, () => true);
  failure();
  failure();
  await fixture.microphone.synchronize({ muted: false, deafened: false });
  assert.equal(fixture.track.stopped, true);
  assert.equal(fixture.track.mediaStreamTrack.enabled, false);
  assert.equal(fixture.microphone.processingAvailable, false);
  assert.equal(fixture.warnings.length, 1);
  assert.deepEqual(fixture.readings.at(-1), { level: 0, active: false, available: false });
});
test('leaving during SDK sender replacement closes its pending public raw stream before adoption', async () => {
  const { microphone, track, room } = harness();
  await microphone.start(room as any, undefined, () => true);
  let reject!: (error: Error) => void;
  const pendingRaw = {
    readyState: 'live',
    stop() {
      this.readyState = 'ended';
    },
  };
  track.restartTrack = async () => {
    (track as any).mediaStream = { getTracks: () => [pendingRaw] };
    // The processor is ready, but sender.replaceTrack has not adopted this raw.
    await new Promise<void>((_resolve, fail) => {
      reject = fail;
    });
  };
  const changing = microphone.configure({ noiseSuppression: false });
  await new Promise((resolve) => setImmediate(resolve));
  await microphone.stop();
  assert.equal(pendingRaw.readyState, 'ended');
  reject(new Error('Sender closed.'));
  await changing;
  assert.equal(microphone.captured, false);
});
test('a rejected SDK sender replacement closes pending raw before rolling capture preferences back', async () => {
  const { microphone, track, room } = harness();
  await microphone.start(room as any, undefined, () => true);
  const pendingRaw = {
    readyState: 'live',
    stop() {
      this.readyState = 'ended';
    },
  };
  let attempts = 0;
  track.restartTrack = async () => {
    if (++attempts === 1) {
      (track as any).mediaStream = { getTracks: () => [pendingRaw] };
      throw new Error('Sender replacement failed.');
    }
    assert.equal(pendingRaw.readyState, 'ended', 'Rollback must not orphan the failed recapture.');
  };
  await assert.rejects(microphone.configure({ noiseSuppression: false }), /настройк/iu);
  assert.equal(attempts, 2);
  assert.equal(pendingRaw.readyState, 'ended');
  assert.equal(microphone.settings.noiseSuppression, true);
  await microphone.stop();
});
