import assert from 'node:assert/strict';
import test from 'node:test';
import { voiceCaptureOptions } from '../src/renderer/media/capture.ts';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';
import { Microphone } from '../src/renderer/media/microphone.ts';

test('healthy neural capture avoids a second denoiser while preserving AEC, AGC, mono48k and the chosen device', () => {
  assert.deepEqual(voiceCaptureOptions(defaultVoiceSettings, 'chosen'), {
    deviceId: { exact: 'chosen' },
    channelCount: 1,
    sampleRate: 48000,
    echoCancellation: true,
    noiseSuppression: false,
    autoGainControl: true,
  });
  assert.equal(voiceCaptureOptions(defaultVoiceSettings, 'chosen', 'browser').noiseSuppression, true);
  assert.equal(
    voiceCaptureOptions({ ...defaultVoiceSettings, noiseSuppression: false }, 'chosen', 'browser')
      .noiseSuppression,
    false,
  );
});

function fallbackHarness(restart: (options: ReturnType<typeof voiceCaptureOptions>) => Promise<void>) {
  const events: string[] = [];
  const track = {
    mediaStreamTrack: { enabled: true },
    async mute() {
      this.mediaStreamTrack.enabled = false;
      events.push('mute');
    },
    async unmute() {
      this.mediaStreamTrack.enabled = true;
      events.push('unmute');
    },
    stop() {
      this.mediaStreamTrack.enabled = false;
      events.push('stop');
    },
    async restartTrack(options: ReturnType<typeof voiceCaptureOptions>) {
      assert.equal(this.mediaStreamTrack.enabled, false);
      events.push(`restart:${options.noiseSuppression}`);
      await restart(options);
    },
  };
  const room = { localParticipant: { async publishTrack() {}, async unpublishTrack() {} } };
  const microphone = new Microphone({
    capture: async () => track as any,
    processor: async () => undefined,
    warning: () => {},
    reading: () => {},
  });
  return { microphone, track, events, room };
}
test('unavailable neural processor restores browser suppression while muted, before any fallback publication can speak', async () => {
  let resume!: () => void;
  const pending = new Promise<void>((resolve) => {
    resume = resolve;
  });
  const h = fallbackHarness(async (options) => {
    assert.equal(options.noiseSuppression, true);
    assert.equal(options.echoCancellation, true);
    await pending;
  });
  const starting = h.microphone.start(h.room as any, 'chosen', () => true);
  await new Promise((resolve) => setImmediate(resolve));
  assert.ok(h.events.includes('restart:true'));
  assert.ok(!h.events.includes('unmute'));
  h.microphone.apply({ muted: true, deafened: false });
  resume();
  await starting;
  assert.equal(h.track.mediaStreamTrack.enabled, false);
  await h.microphone.configure({ autoGainControl: false });
  assert.equal(h.microphone.settings.noiseSuppression, true);
  await h.microphone.stop();
});
test('cancelling a browser-suppression recovery cannot revive the microphone or leave capture open', async () => {
  let resume!: () => void;
  const pending = new Promise<void>((resolve) => {
    resume = resolve;
  });
  const h = fallbackHarness(async () => pending);
  const starting = h.microphone.start(h.room as any, undefined, () => true);
  await new Promise((resolve) => setImmediate(resolve));
  assert.ok(h.events.includes('restart:true'));
  await h.microphone.stop();
  resume();
  await starting;
  assert.equal(h.track.mediaStreamTrack.enabled, false);
  assert.ok(h.events.includes('stop'));
  assert.ok(!h.events.includes('unmute'));
});
test('failed browser-suppression recovery stops capture instead of transmitting unfiltered fallback audio', async () => {
  const h = fallbackHarness(async () => {
    throw new Error('Capture unavailable.');
  });
  await h.microphone.start(h.room as any, undefined, () => true);
  assert.ok(h.events.includes('restart:true'));
  assert.ok(h.events.includes('stop'));
  assert.ok(!h.events.includes('unmute'));
  assert.equal(h.microphone.captured, false);
});
