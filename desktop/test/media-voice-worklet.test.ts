import assert from 'node:assert/strict';
import test from 'node:test';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';

const messages: unknown[] = [];
let registered!: new (options: unknown) => {
  process(inputs: Float32Array[][], outputs: Float32Array[][]): boolean;
  port: { onmessage(event: { data: unknown }): void };
};
Object.assign(globalThis, {
  currentFrame: 0,
  sampleRate: 48000,
  AudioWorkletProcessor: class {
    port = { onmessage: null, postMessage: (message: unknown) => messages.push(message) };
  },
  registerProcessor: (_name: string, processor: typeof registered) => {
    registered = processor;
  },
});
await import('../src/renderer/media/voice-worklet.ts');

test('actual worklet applies input gain once and sends only bounded meter metadata at 20Hz', () => {
  const processor = new registered({
    processorOptions: { ...defaultVoiceSettings, noiseSuppression: false, inputGain: 1.5 },
  });
  const input = new Float32Array(128).fill(0.02);
  const output = new Float32Array(128);
  messages.length = 0;
  for (let frame = 0; frame < 48000; frame += 128) {
    Object.assign(globalThis, { currentFrame: frame });
    assert.equal(processor.process([[input]], [[output]]), true);
  }
  assert.ok(Math.abs(output[127] - 0.03) < 0.0001);
  assert.equal(messages.length, 20);
  for (const message of messages) {
    assert.deepEqual(Object.keys(message as object).sort(), ['active', 'level', 'type']);
    assert.ok((message as any).level >= 0 && (message as any).level <= 1);
  }
});
test('VAD rejects quiet audio, receives valid settings and keeps malformed samples finite', () => {
  const processor = new registered({
    processorOptions: {
      ...defaultVoiceSettings,
      noiseSuppression: false,
      mode: 'vad',
      thresholdDb: -30,
      holdMs: 0,
    },
  }) as any;
  const output = new Float32Array(128);
  Object.assign(globalThis, { currentFrame: 0 });
  processor.process([[new Float32Array(128).fill(0.001)]], [[output]]);
  assert.equal(
    output.every((sample) => sample === 0),
    true,
  );
  processor.port.onmessage({
    data: {
      type: 'settings',
      settings: { ...defaultVoiceSettings, noiseSuppression: false, mode: 'continuous' },
    },
  });
  for (let frame = 0; frame < 4096; frame += 128) {
    Object.assign(globalThis, { currentFrame: frame });
    processor.process([[new Float32Array(128).fill(0.1)]], [[output]]);
  }
  assert.ok(output[127] > 0.099);
  processor.process([[new Float32Array(128).fill(NaN)]], [[output]]);
  assert.equal(output.every(Number.isFinite), true);
  processor.process([], [[output]]);
  assert.equal(
    output.every((sample) => sample === 0),
    true,
  );
});
test('48k worklet initializes neural suppression and destroy closes processing permanently', () => {
  Object.assign(globalThis, { sampleRate: 48000, currentFrame: 0 });
  messages.length = 0;
  const processor = new registered({ processorOptions: defaultVoiceSettings });
  assert.deepEqual(messages[0], { type: 'ready', neuralNoise: true, sampleRate: 48000 });
  const output = new Float32Array(128);
  for (let frame = 0; frame < 4800; frame += 128) {
    Object.assign(globalThis, { currentFrame: frame });
    assert.equal(processor.process([[new Float32Array(128).fill(0.01)]], [[output]]), true);
    assert.equal(output.every(Number.isFinite), true);
  }
  processor.port.onmessage({ data: { type: 'destroy' } });
  processor.port.onmessage({ data: { type: 'settings', settings: defaultVoiceSettings } });
  output.fill(1);
  assert.equal(processor.process([[new Float32Array(128).fill(0.1)]], [[output]]), false);
  assert.equal(
    output.every((value) => value === 0),
    true,
  );
  assert.equal(
    messages.every((value) => !('pcm' in (value as object))),
    true,
  );
});
test('a non-48k worklet keeps gain and VAD available without running a model at the wrong rate', () => {
  Object.assign(globalThis, { sampleRate: 44100, currentFrame: 0 });
  messages.length = 0;
  const processor = new registered({
    processorOptions: { ...defaultVoiceSettings, inputGain: 1.5 },
  });
  assert.deepEqual(messages[0], { type: 'ready', neuralNoise: false, sampleRate: 44100 });
  const output = new Float32Array(128);
  for (let frame = 0; frame < 4410; frame += 128) {
    Object.assign(globalThis, { currentFrame: frame });
    processor.process([[new Float32Array(128).fill(0.02)]], [[output]]);
  }
  assert.ok(Math.abs(output[127] - 0.03) < 0.0001);
  processor.port.onmessage({ data: { type: 'destroy' } });
  Object.assign(globalThis, { sampleRate: 48000 });
});
