/** Hermetic speech-plus-noise benchmark. No user recordings or pure-tone NS assertions. */
export function noiseSpeechFixture(input: Buffer, noisy = true) {
  if (
    input.length < 44 ||
    input.toString('ascii', 0, 4) !== 'RIFF' ||
    input.toString('ascii', 8, 12) !== 'WAVE'
  )
    throw new Error('Invalid speech fixture.');
  let data: Buffer | undefined;
  let validFormat = false;
  for (let offset = 12; offset + 8 <= input.length;) {
    const kind = input.toString('ascii', offset, offset + 4);
    const size = input.readUInt32LE(offset + 4);
    const start = offset + 8;
    if (size > input.length - start) throw new Error('Invalid speech fixture.');
    if (kind === 'fmt ' && size >= 16)
      validFormat =
        input.readUInt16LE(start) === 1 &&
        input.readUInt16LE(start + 2) === 1 &&
        input.readUInt32LE(start + 4) === 48000 &&
        input.readUInt16LE(start + 14) === 16;
    if (kind === 'data') data = input.subarray(start, start + size);
    offset = start + size + (size % 2);
  }
  if (!validFormat || !data?.length || data.length % 2) throw new Error('Invalid speech fixture.');
  const speech = Float32Array.from(
    { length: data.length / 2 },
    (_, index) => data!.readInt16LE(index * 2) / 32768,
  );
  const speechEnergy = speech.reduce((sum, sample) => sum + sample * sample, 0) / speech.length;
  if (speechEnergy < 0.00001) throw new Error('Invalid speech fixture.');
  const padding = 4 * 48000;
  const frames = speech.length + padding * 2;
  const noise = new Float32Array(frames);
  let seed = 0x123ab789;
  let previous = 0;
  for (let index = 0; index < frames; index++) {
    seed ^= seed << 13;
    seed ^= seed >>> 17;
    seed ^= seed << 5;
    previous = previous * 0.85 + ((seed >>> 0) / 0x100000000 - 0.5) * 0.15;
    noise[index] = previous;
  }
  const noiseEnergy = noise.reduce((sum, sample) => sum + sample * sample, 0) / frames;
  const noiseGain = 0.025 / Math.sqrt(noiseEnergy);
  const speechGain = 0.07 / Math.sqrt(speechEnergy);
  const keys = [
    0.6,
    1.1,
    1.8,
    2.8,
    frames / 48000 - 3.4,
    frames / 48000 - 2.9,
    frames / 48000 - 2.2,
    frames / 48000 - 1.2,
  ].map((seconds) => Math.round(seconds * 48000));
  const speechKeys = [0.4, 1.5, 2.8, 3.7].map((seconds) => padding + Math.round(seconds * 48000));
  const clicks = new Float32Array(frames);
  for (const key of [...keys, ...speechKeys])
    for (let index = 0; index < 2160; index++) {
      seed ^= seed << 13;
      seed ^= seed >>> 17;
      seed ^= seed << 5;
      // Broadband impacts with a short decaying tail model a key strike/release;
      // they are deterministic transient proxies, not a recording of a real keyboard.
      const tail = Math.exp(-index / 400) + (index > 1440 ? 0.5 * Math.exp(-(index - 1440) / 240) : 0);
      clicks[key + index] = ((seed >>> 0) / 0x100000000 - 0.5) * 0.5 * tail;
    }
  const envelope: number[] = [];
  for (let offset = 0; offset < frames; offset += 1024) {
    let energy = 0;
    for (let index = 0; index < 1024; index++) energy += (speech[offset + index - padding] ?? 0) ** 2;
    envelope.push(Math.sqrt(energy / 1024) * speechGain);
  }
  const wav = Buffer.alloc(44 + frames * 2);
  wav.write('RIFF', 0);
  wav.writeUInt32LE(wav.length - 8, 4);
  wav.write('WAVEfmt ', 8);
  wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(1, 20);
  wav.writeUInt16LE(1, 22);
  wav.writeUInt32LE(48000, 24);
  wav.writeUInt32LE(96000, 28);
  wav.writeUInt16LE(2, 32);
  wav.writeUInt16LE(16, 34);
  wav.write('data', 36);
  wav.writeUInt32LE(frames * 2, 40);
  for (let index = 0; index < frames; index++) {
    const sample =
      (speech[index - padding] ?? 0) * speechGain + (noisy ? noise[index] * noiseGain + clicks[index] : 0);
    if (Math.abs(sample) >= 0.95) throw new Error('Clipping speech fixture.');
    wav.writeInt16LE(Math.round(sample * 32767), 44 + index * 2);
  }
  return {
    wav,
    seconds: frames / 48000,
    noiseRms: 0.025,
    speechRms: 0.07,
    reference: { envelope, frames, keys, speechKeys },
  };
}

/** Percentiles span a full input loop containing noise-only pauses and real speech.
 * Relative floor separates attenuation from NS, without claiming perceptual speech scores.
 */
export function voiceDistribution(levels: readonly number[]) {
  if (levels.length < 20 || levels.some((value) => !Number.isFinite(value) || value < 0))
    throw new Error('Invalid voice samples.');
  const values = [...levels].sort((a, b) => a - b);
  const db = (value: number) => 20 * Math.log10(Math.max(0.00000001, value));
  const quietDb = db(values[Math.floor(values.length * 0.2)]);
  const speechDb = db(values[Math.floor(values.length * 0.9)]);
  return { quietDb, speechDb, relativeNoiseDb: quietDb - speechDb, frames: values.length };
}

/** Align a received RMS envelope with the public speech loop. Stream startup and
 * TURN delays may shift it; no timestamps or speech samples leave the test process.
 * Regional levels separate fan and short impacts without claiming perceptual scores.
 */
export function voiceRegions(
  levels: readonly number[],
  step: number,
  reference: { envelope: readonly number[]; frames: number; keys: readonly number[] },
) {
  voiceDistribution(levels);
  if (!Number.isFinite(step) || step <= 0 || step > 0.1) throw new Error('Invalid voice samples.');
  const mean = levels.reduce((sum, level) => sum + level, 0) / levels.length;
  let correlation = -1;
  let shift = 0;
  for (let candidate = 0; candidate < reference.envelope.length; candidate++) {
    const expected = levels.map(
      (_, index) =>
        reference.envelope[
          Math.floor(((candidate * 1024 + index * step * 48000 + 1024) % reference.frames) / 1024)
        ],
    );
    const expectedMean = expected.reduce((sum, level) => sum + level, 0) / expected.length;
    let cross = 0,
      receivedEnergy = 0,
      expectedEnergy = 0;
    for (let index = 0; index < levels.length; index++) {
      const x = expected[index] - expectedMean;
      const y = levels[index] - mean;
      cross += x * y;
      expectedEnergy += x * x;
      receivedEnergy += y * y;
    }
    const score = cross / Math.max(1e-20, Math.sqrt(expectedEnergy * receivedEnergy));
    if (score > correlation) {
      correlation = score;
      shift = candidate * 1024;
    }
  }
  const regions: Record<'fan' | 'keys' | 'speech', number[]> = { fan: [], keys: [], speech: [] };
  levels.forEach((level, index) => {
    const frame = (shift + index * step * 48000 + 1024) % reference.frames;
    const clean = reference.envelope[Math.floor(frame / 1024)];
    if (clean > 0.035) regions.speech.push(level);
    else if (reference.keys.some((key) => Math.abs(key - frame) < 4300)) regions.keys.push(level);
    else if (clean === 0) regions.fan.push(level);
  });
  const percentile = (values: readonly number[], quantile: number) => {
    if (values.length < 10) throw new Error('Invalid voice regions.');
    const sorted = [...values].sort((a, b) => a - b);
    return 20 * Math.log10(Math.max(1e-8, sorted[Math.floor(sorted.length * quantile)]));
  };
  return {
    correlation,
    fanDb: percentile(regions.fan, 0.5),
    keysDb: percentile(regions.keys, 0.9),
    speechDb: percentile(regions.speech, 0.9),
  };
}
