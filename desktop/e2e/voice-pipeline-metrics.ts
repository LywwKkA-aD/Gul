/** Test-only waveform residual proxy. It is not a perceptual speech-quality score. */
export function speechFidelity(reference: Float32Array, received: Float32Array) {
  if (
    reference.length < 8192 ||
    received.length < 8192 ||
    reference.length > 48_000 * 60 ||
    received.length > 48_000 * 60 ||
    reference.some((sample) => !Number.isFinite(sample)) ||
    received.some((sample) => !Number.isFinite(sample))
  )
    throw new Error('Invalid speech samples.');
  const envelope = (input: Float32Array, stride: number) => {
    const values = new Float32Array(Math.floor(input.length / stride));
    for (let block = 0; block < values.length; block++) {
      let energy = 0;
      for (let frame = 0; frame < 512; frame++) energy += (input[block * stride + frame] ?? 0) ** 2;
      values[block] = Math.sqrt(energy / 512);
    }
    return values;
  };
  const cleanLevels = envelope(reference, 256);
  const levels = envelope(received, 512);
  let cleanPeak = 0;
  let receivedEnergy = 0;
  for (const value of cleanLevels) cleanPeak = Math.max(cleanPeak, value);
  for (const value of levels) receivedEnergy += value * value;
  if (!cleanPeak || !receivedEnergy) throw new Error('Missing speech.');
  let coarse = 0;
  let score = Number.NEGATIVE_INFINITY;
  for (let offset = 0; offset < cleanLevels.length; offset++) {
    let cross = 0;
    let energy = 0;
    for (let block = 0; block < levels.length; block++) {
      const clean = cleanLevels[(offset + block * 2) % cleanLevels.length];
      cross += clean * levels[block];
      energy += clean * clean;
    }
    const current = cross / Math.max(1e-20, Math.sqrt(energy * receivedEnergy));
    if (current > score) {
      score = current;
      coarse = offset * 256;
    }
  }
  let anchor = 0;
  let anchorEnergy = 0;
  const window = 4096;
  for (let offset = 0; offset + window < reference.length; offset += 512) {
    let energy = 0;
    for (let frame = 0; frame < window; frame++) energy += reference[offset + frame] ** 2;
    if (energy > anchorEnergy) {
      anchor = offset;
      anchorEnergy = energy;
    }
  }
  const remoteAnchor = (anchor - coarse + reference.length) % reference.length;
  if (remoteAnchor + window > received.length) throw new Error('Missing aligned speech.');
  let aligned = coarse;
  let best = Number.NEGATIVE_INFINITY;
  for (let delta = -4096; delta <= 4096; delta++) {
    const candidate = (coarse + delta + reference.length) % reference.length;
    let cross = 0;
    let clean = 0;
    let remote = 0;
    for (let frame = 0; frame < window; frame++) {
      const x = reference[(candidate + remoteAnchor + frame) % reference.length];
      const y = received[remoteAnchor + frame];
      cross += x * y;
      clean += x * x;
      remote += y * y;
    }
    const current = Math.abs(cross) / Math.max(1e-20, Math.sqrt(clean * remote));
    if (current > best) {
      best = current;
      aligned = candidate;
    }
  }
  let clean = 0;
  let remote = 0;
  let cross = 0;
  for (let frame = 0; frame < received.length; frame++) {
    const index = (aligned + frame) % reference.length;
    if (cleanLevels[Math.floor(index / 256)] < cleanPeak * 0.08) continue;
    const x = reference[index];
    const y = received[frame];
    clean += x * x;
    remote += y * y;
    cross += x * y;
  }
  const gain = cross / Math.max(1e-20, clean);
  const signal = gain * gain * clean;
  const residual = Math.max(1e-20, remote - signal);
  return Object.freeze({
    alignmentSamples: aligned,
    correlation: Math.abs(cross) / Math.max(1e-20, Math.sqrt(clean * remote)),
    residualDb: Math.min(120, 10 * Math.log10(Math.max(1e-20, signal) / residual)),
    levelDb: 10 * Math.log10(Math.max(1e-20, remote / clean)),
  });
}
