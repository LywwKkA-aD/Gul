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
  track.applyConstraints = async () => {
    throw new Error('device is gone');
  };
  await assert.rejects(microphone.configure({ echoCancellation: false }), /настройк/iu);
  assert.equal(microphone.settings.echoCancellation, true);
  assert.equal(track.mediaStreamTrack.enabled, false);
  await microphone.stop();
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
