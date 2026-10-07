import assert from 'node:assert/strict';
import test from 'node:test';
import { defaultVoiceSettings, voiceSettings, VoiceGate } from '../src/renderer/media/voice-gate.ts';

const samples = (amplitude: number) => new Float32Array(128).fill(amplitude);
test('VAD closes silence, opens speech and holds the tail without changing manual mute', () => {
  const gate = new VoiceGate({ ...defaultVoiceSettings, mode: 'vad', thresholdDb: -40, holdMs: 200 });
  assert.equal(gate.read(samples(0), 0).gain, 0);
  assert.equal(gate.read(samples(0.02), 10).gain, 1);
  assert.equal(gate.read(samples(0), 100).gain, 1);
  assert.equal(gate.read(samples(0), 211).gain, 0);
  assert.equal(gate.read(samples(0), 500).active, false);
});
test('continuous transmission preserves quiet samples and applies input gain exactly once', () => {
  const gate = new VoiceGate({ ...defaultVoiceSettings, inputGain: 1.5 });
  const reading = gate.read(samples(0.02), 0);
  assert.equal(reading.gain, 1.5);
  assert.ok(Math.abs(reading.level - 0.03) < 0.0001);
  assert.equal(gate.read(samples(0), 1000).gain, 1.5);
});
test('invalid samples and clock changes are fail-closed; switching mode clears old hangover', () => {
  const gate = new VoiceGate({ ...defaultVoiceSettings, mode: 'vad' });
  gate.read(samples(0.1), 100);
  assert.equal(gate.read(samples(0), 0).gain, 0);
  assert.equal(gate.read(samples(Number.NaN), 1).gain, 0);
  assert.equal(gate.read(samples(0.1), Number.NaN).gain, 0);
  gate.update({ ...defaultVoiceSettings, mode: 'continuous' });
  assert.equal(gate.read(samples(0), 2).gain, 1);
  gate.update({ ...defaultVoiceSettings, mode: 'vad' });
  assert.equal(gate.read(samples(0), 3).gain, 0);
});
test('voice preferences validate bounded values and preserve immutable settings', () => {
  const settings = voiceSettings(defaultVoiceSettings, {
    inputGain: 2,
    thresholdDb: -55,
    holdMs: 300,
    echoCancellation: false,
  });
  assert.equal(settings.inputGain, 2);
  assert.equal(defaultVoiceSettings.inputGain, 1);
  assert.equal(Object.isFrozen(settings), true);
  for (const patch of [
    { inputGain: 3 },
    { inputGain: NaN },
    { thresholdDb: -90 },
    { holdMs: -1 },
    { mode: 'other' },
    { noiseSuppression: 'yes' },
  ]) {
    assert.throws(() => voiceSettings(settings, patch as any), /настройк/iu);
  }
});
