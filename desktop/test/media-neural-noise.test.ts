import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { NeuralDenoiser } from '../src/renderer/media/neural-noise.ts';
import { createNeuralDenoiser } from '../src/renderer/media/neural-runtime.ts';
import { noiseSpeechFixture, voiceRegions } from '../e2e/voice-noise-fixture.ts';

function runtime() {
  const calls = { create: 0, allocate: 0, process: 0, free: 0, destroy: 0 };
  const model = {
    HEAPF32: new Float32Array(4096),
    _rnnoise_create() {
      calls.create++;
      return 1;
    },
    _malloc() {
      calls.allocate++;
      return 32;
    },
    _rnnoise_process_frame(_state: number, output: number, input: number) {
      calls.process++;
      for (let index = 0; index < 480; index++)
        model.HEAPF32[output / 4 + index] = model.HEAPF32[input / 4 + index] * 0.5;
      return 0.9;
    },
    _rnnoise_destroy() {
      calls.destroy++;
    },
    _free() {
      calls.free++;
    },
  };
  return { model, calls };
}
test('128-frame worklet blocks reach 480-frame neural processing without gaps, duplication or repeated allocations', () => {
  const { model, calls } = runtime();
  const denoiser = new NeuralDenoiser(model);
  const result: number[] = [];
  for (let frame = 0; frame < 1024; frame += 128) {
    const input = Float32Array.from({ length: 128 }, (_, index) => (frame + index + 1) / 2048);
    const output = new Float32Array(128);
    denoiser.process(input, output);
    result.push(...output);
  }
  assert.ok(result.slice(0, 479).every((sample) => sample === 0));
  for (let index = 479; index < result.length; index++) assert.equal(result[index], (index - 478) / 4096);
  assert.deepEqual(calls, { create: 1, allocate: 1, process: 2, free: 0, destroy: 0 });
  denoiser.destroy();
  denoiser.destroy();
  const output = new Float32Array(128).fill(1);
  denoiser.process(new Float32Array(128).fill(1), output);
  assert.ok(output.every((sample) => sample === 0));
  assert.equal(calls.free, 1);
  assert.equal(calls.destroy, 1);
  assert.equal(calls.process, 2);
});
test('malformed input stays finite and partial model allocation failures release owned state', () => {
  const { model, calls } = runtime();
  const denoiser = new NeuralDenoiser(model);
  const output = new Float32Array(1024);
  denoiser.process(
    Float32Array.from({ length: 1024 }, (_, index) => (index % 2 ? Infinity : NaN)),
    output,
  );
  assert.ok(output.every((sample) => sample === 0));
  denoiser.destroy();
  model._malloc = () => 0;
  assert.throws(() => new NeuralDenoiser(model), /noise processing/);
  assert.equal(calls.destroy, 2);
  model._rnnoise_create = () => 0;
  assert.throws(() => new NeuralDenoiser(model), /noise processing/);
});
test('pinned neural model suppresses fan/key proxies during real speech while preserving the clean reference', async (t) => {
  const speech = await readFile(new URL('../e2e/testdata/noise/clean-speech.wav', import.meta.url));
  const noisy = noiseSpeechFixture(speech),
    clean = noiseSpeechFixture(speech, false);
  assert.equal(noisy.reference.speechKeys.length, 4);
  const raw = (wav: Buffer) =>
    Float32Array.from(
      { length: noisy.reference.frames },
      (_, index) => wav.readInt16LE(44 + index * 2) / 32768,
    );
  const filtered = (input: Float32Array) => {
    const model = createNeuralDenoiser();
    const output = new Float32Array(Math.ceil(input.length / 128) * 128);
    for (let index = 0; index < input.length; index += 128)
      model.process(input.subarray(index, index + 128), output.subarray(index, index + 128));
    model.destroy();
    return output;
  };
  const cleanInput = raw(clean.wav),
    noisyInput = raw(noisy.wav);
  const cleanOutput = filtered(cleanInput),
    noisyOutput = filtered(noisyInput);
  const fidelity = (reference: Float32Array, received: Float32Array, delay: number) => {
    let original = 0,
      error = 0,
      cross = 0,
      signal = 0;
    for (let index = 0; index < noisy.reference.frames; index++) {
      if (noisy.reference.envelope[Math.floor(Math.max(0, index - delay) / 1024)] <= 0.035) continue;
      original += reference[index] ** 2;
      signal += received[index] ** 2;
      cross += reference[index] * received[index];
      error += (reference[index] - received[index]) ** 2;
    }
    return {
      errorDb: 10 * Math.log10(original / error),
      correlation: cross / Math.sqrt(original * signal),
      levelDb: 10 * Math.log10(signal / original),
    };
  };
  const before = fidelity(cleanInput, noisyInput, 0),
    after = fidelity(cleanOutput, noisyOutput, 1439);
  const aligned = Float32Array.from(
    { length: cleanInput.length },
    (_, index) => cleanInput[index - 1439] ?? 0,
  );
  const preservation = fidelity(aligned, noisyOutput, 1439);
  assert.ok(after.errorDb > before.errorDb + 3, `Speech residual comparison failed: ${after.errorDb}.`);
  assert.ok(after.correlation > 0.9);
  assert.ok(Math.abs(after.levelDb) < 3);
  assert.ok(preservation.correlation > 0.9);
  assert.ok(Math.abs(preservation.levelDb) < 3);
  const quietClean = cleanInput.map((sample) => sample * 0.2);
  const quiet = filtered(noisyInput.map((sample) => sample * 0.2));
  const quietAligned = Float32Array.from(
    { length: quietClean.length },
    (_, index) => quietClean[index - 1439] ?? 0,
  );
  const quietPreservation = fidelity(quietAligned, quiet, 1439);
  assert.ok(quietPreservation.correlation > 0.85);
  assert.ok(Math.abs(quietPreservation.levelDb) < 3);
  t.diagnostic(
    JSON.stringify({ baseline: before, neural: after, preserved: preservation, quiet: quietPreservation }),
  );
  const envelope = (input: Float32Array) =>
    Array.from({ length: Math.floor(input.length / 2048) }, (_, frame) => {
      let energy = 0;
      for (let index = 0; index < 2048; index++) energy += input[frame * 2048 + index] ** 2;
      return Math.sqrt(energy / 2048);
    });
  const original = voiceRegions(envelope(noisyInput), 2048 / 48000, noisy.reference);
  const denoised = voiceRegions(envelope(noisyOutput), 2048 / 48000, noisy.reference);
  assert.ok(denoised.keysDb < original.keysDb - 15);
  assert.ok(denoised.fanDb < original.fanDb - 20);
  assert.ok(noisyOutput.every((sample) => Number.isFinite(sample) && Math.abs(sample) < 0.99));
});
