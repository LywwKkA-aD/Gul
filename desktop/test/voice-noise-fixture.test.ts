import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { noiseSpeechFixture, voiceDistribution, voiceRegions } from '../e2e/voice-noise-fixture.ts';

test('speech fixture preserves real words, adds stationary non-tone noise and stays below clipping', async () => {
  const clean = await readFile(new URL('../e2e/testdata/noise/clean-speech.wav', import.meta.url));
  const fixture = noiseSpeechFixture(clean);
  assert.ok(fixture.seconds > 13 && fixture.seconds < 15);
  assert.ok(fixture.noiseRms > 0.024 && fixture.noiseRms < 0.026);
  assert.ok(fixture.speechRms > 0.069 && fixture.speechRms < 0.071);
  const samples = fixture.wav.subarray(44);
  let peak = 0;
  for (let index = 0; index < samples.length; index += 2)
    peak = Math.max(peak, Math.abs(samples.readInt16LE(index) / 32768));
  assert.ok(peak < 0.95);
  assert.equal(fixture.wav.readUInt32LE(24), 48000);
  assert.equal(fixture.wav.readUInt16LE(22), 1);
  assert.equal(samples.length / 2 / 48000, fixture.seconds);
  assert.deepEqual(
    noiseSpeechFixture(clean).wav,
    fixture.wav,
    'deterministic input supports repeatable comparisons',
  );
});
test('transient keyboard proxies are measured separately from fan and speech after an unknown stream offset', async () => {
  const fixture = noiseSpeechFixture(
    await readFile(new URL('../e2e/testdata/noise/clean-speech.wav', import.meta.url)),
  );
  assert.equal(fixture.reference.keys.length, 8);
  const levels = Array.from({ length: 350 }, (_, frame) => {
    const offset = (frame * 2048 + 91000) % fixture.reference.frames;
    let energy = 0;
    for (let index = 0; index < 2048; index++) {
      const sample = fixture.wav.readInt16LE(44 + ((offset + index) % fixture.reference.frames) * 2) / 32768;
      energy += sample * sample;
    }
    return Math.sqrt(energy / 2048);
  });
  const regions = voiceRegions(levels, 2048 / 48000, fixture.reference);
  assert.ok(regions.correlation > 0.9);
  assert.ok(regions.keysDb > regions.fanDb + 2);
  assert.ok(regions.speechDb > regions.fanDb + 8);
  const scaled = voiceRegions(
    levels.map((level) => level * 2),
    2048 / 48000,
    fixture.reference,
  );
  assert.ok(Math.abs(scaled.keysDb - regions.keysDb - 6.0206) < 0.001);
  assert.throws(() => voiceRegions([], 2048 / 48000, fixture.reference), /voice samples/);
});

test('noise benchmark rejects malformed or unsupported speech instead of measuring synthetic silence', () => {
  for (const input of [Buffer.alloc(44), Buffer.from('RIFF'), Buffer.alloc(100)])
    assert.throws(() => noiseSpeechFixture(input), /speech fixture/);
});

test('voice distribution measures quiet-to-speech separation independently from a common gain', () => {
  const levels = [...Array.from({ length: 70 }, () => 0.01), ...Array.from({ length: 30 }, () => 0.1)];
  const original = voiceDistribution(levels);
  const amplified = voiceDistribution(levels.map((value) => value * 2));
  assert.equal(original.quietDb, -40);
  assert.equal(original.speechDb, -20);
  assert.equal(original.relativeNoiseDb, -20);
  assert.equal(amplified.relativeNoiseDb, original.relativeNoiseDb);
  assert.ok(Math.abs(amplified.speechDb - original.speechDb - 6.0206) < 0.001);
  assert.throws(() => voiceDistribution([NaN, Infinity]), /voice samples/);
  assert.equal(voiceDistribution(Array.from({ length: 30 }, () => 0)).relativeNoiseDb, 0);
});
