import test from 'node:test';
import assert from 'node:assert/strict';
import { ScreenPCM } from '../src/renderer/media/screen-pcm.ts';

function packet(sequence = 0, frames = 480, flags = 0): ArrayBuffer {
  const buffer = new ArrayBuffer(16 + frames * 8);
  const data = new DataView(buffer);
  [0x314c5547, sequence, frames, flags].forEach((value, index) => data.setUint32(index * 4, value, true));
  for (let frame = 0; frame < frames; frame++) {
    data.setFloat32(16 + frame * 8, 0.25, true);
    data.setFloat32(20 + frame * 8, -0.125, true);
  }
  return buffer;
}
test('screen PCM preserves independent stereo samples after a bounded startup buffer', () => {
  const pcm = new ScreenPCM();
  const left = new Float32Array(128);
  const right = new Float32Array(128);
  pcm.push(packet(0));
  pcm.read(left, right);
  assert(left.every((sample) => sample === 0));
  pcm.push(packet(1));
  pcm.read(left, right);
  assert(left.every((sample) => sample === 0.25));
  assert(right.every((sample) => sample === -0.125));
});
test('PCM rejects malformed, non-finite, reordered or overflowing frames without unbounded storage', () => {
  for (const mode of ['magic', 'count', 'flags', 'nan', 'range', 'length'] as const) {
    const buffer = packet();
    const data = new DataView(buffer);
    if (mode === 'magic') data.setUint32(0, 0, true);
    if (mode === 'count') data.setUint32(8, 481, true);
    if (mode === 'flags') data.setUint32(12, 2, true);
    if (mode === 'nan') data.setFloat32(16, Number.NaN, true);
    if (mode === 'range') data.setFloat32(16, 2, true);
    assert.throws(() => new ScreenPCM().push(mode === 'length' ? buffer.slice(0, 16) : buffer));
  }
  const pcm = new ScreenPCM();
  pcm.push(packet(0));
  assert.throws(() => pcm.push(packet(0)));
  assert.throws(() => pcm.push(packet(2)));
  const bounded = new ScreenPCM();
  for (let index = 0; index < 12; index++) bounded.push(packet(index));
  assert.throws(() => bounded.push(packet(12)), /GUL_SCREEN_AUDIO_BUFFER/);
});
test('discontinuity discards stale audio and silence underruns never replay previous samples', () => {
  const pcm = new ScreenPCM();
  pcm.push(packet(0));
  pcm.push(packet(10, 480, 1));
  const left = new Float32Array(480);
  const right = new Float32Array(480);
  pcm.read(left, right);
  assert(left.every((sample) => sample === 0));
  pcm.push(packet(11));
  pcm.read(left, right);
  pcm.read(left, right);
  pcm.read(left, right);
  assert(left.every((sample) => sample === 0));
  assert(right.every((sample) => sample === 0));
});
