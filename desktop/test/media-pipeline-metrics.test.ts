import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { speechFidelity } from '../e2e/voice-pipeline-metrics.ts';
import { noiseSpeechFixture } from '../e2e/voice-noise-fixture.ts';

const samples = 48_000;
function speech() {
  return Float32Array.from({ length: samples }, (_, index) => {
    if (index < 8000 || index > 36_000) return 0;
    const envelope = Math.sin(((index - 8000) * Math.PI) / 28_000) ** 2;
    return envelope * 0.1 * (Math.sin(index * 0.031 + index * index * 0.000001) + Math.sin(index * 0.17));
  });
}
test('speech metrics align decoded delay and separate amplitude scaling from added noise', () => {
  const clean = speech();
  const received = Float32Array.from(
    { length: samples },
    (_, index) => clean[(index + 13_731) % samples] * 0.4,
  );
  const result = speechFidelity(clean, received);
  assert.ok(Math.abs(result.alignmentSamples - 13_731) <= 1);
  assert.ok(result.residualDb > 80);
  assert.ok(result.correlation > 0.999);
  assert.ok(Math.abs(result.levelDb - 20 * Math.log10(0.4)) < 0.05);
  const noisy = received.map((value, index) => value + 0.005 * Math.sin(index * 1.17));
  const noise = speechFidelity(clean, noisy);
  assert.ok(noise.residualDb < result.residualDb - 50);
  assert.ok(noise.correlation > 0.9);
});
test('missing speech and invalid received samples cannot become favorable quality scores', () => {
  assert.throws(() => speechFidelity(new Float32Array(samples), speech()), /speech/);
  assert.throws(() => speechFidelity(speech(), new Float32Array(samples)), /speech/);
  const invalid = speech();
  invalid[512] = Infinity;
  assert.throws(() => speechFidelity(speech(), invalid), /samples/);
  assert.throws(() => speechFidelity(speech(), new Float32Array(10)), /samples/);
});
test('quiet speech fixture lowers the voice while preserving the exact fan and keyboard noise', async () => {
  const wav = await readFile(new URL('../e2e/testdata/noise/clean-speech.wav', import.meta.url));
  const normal = noiseSpeechFixture(wav);
  const quiet = noiseSpeechFixture(wav, true, 0.2);
  assert.equal(quiet.seconds, normal.seconds);
  assert.equal(quiet.noiseRms, normal.noiseRms);
  assert.equal(quiet.speechRms, normal.speechRms * 0.2);
  assert.deepEqual(quiet.wav.subarray(44, 44 + 4 * 48_000 * 2), normal.wav.subarray(44, 44 + 4 * 48_000 * 2));
  assert.deepEqual(
    quiet.reference.envelope,
    normal.reference.envelope.map((value) => value * 0.2),
  );
  assert.throws(() => noiseSpeechFixture(wav, true, NaN), /speech scale/);
});
