import assert from 'node:assert/strict';
import test from 'node:test';
import { WindowsAudioFrames } from '../src/main/windows-audio-protocol.ts';

function header() {
  const buffer = Buffer.alloc(24);
  buffer.write('GULAUD1\0', 0, 'ascii');
  [48000, 2, 1, 480].forEach((value, index) => buffer.writeUInt32LE(value, 8 + index * 4));
  return buffer;
}
function frame(sequence = 0, flags = 0, samples: readonly number[] = [0.1, -0.2]) {
  const buffer = Buffer.alloc(16 + samples.length * 4);
  buffer.writeUInt32LE(0x314c5547, 0);
  buffer.writeUInt32LE(sequence, 4);
  buffer.writeUInt32LE(samples.length / 2, 8);
  buffer.writeUInt32LE(flags, 12);
  samples.forEach((value, index) => buffer.writeFloatLE(value, 16 + index * 4));
  return buffer;
}
function fixture() {
  let ready = 0;
  const frames: Buffer[] = [];
  const parser = new WindowsAudioFrames(
    () => ++ready,
    (buffer) => frames.push(buffer),
  );
  return { parser, frames, ready: () => ready };
}
test('native PCM parser handles arbitrary pipe fragmentation and preserves bounded stereo frames', () => {
  const f = fixture();
  const data = Buffer.concat([header(), frame(), frame(1)]);
  for (const byte of data) f.parser.push(Buffer.from([byte]));
  assert.equal(f.ready(), 1);
  assert.equal(f.frames.length, 2);
  assert.equal(f.frames[0].readUInt32LE(8), 1);
  assert.ok(Math.abs(f.frames[0].readFloatLE(16) - 0.1) < 1e-6);
});
test('native PCM rejects wrong versions, formats, sizes and invalid amplitudes before delivery', () => {
  for (const offset of [0, 8, 12, 16, 20]) {
    const f = fixture();
    const bad = header();
    bad[offset] ^= 1;
    assert.throws(() => f.parser.push(bad), /GUL_AUDIO_PROTOCOL/);
    assert.equal(f.ready(), 0);
  }
  for (const mutate of [
    (bad: Buffer) => bad.writeUInt32LE(2, 12),
    (bad: Buffer) => bad.writeUInt32LE(0, 8),
    (bad: Buffer) => bad.writeUInt32LE(481, 8),
    (bad: Buffer) => bad.writeUInt32LE(0, 0),
    (bad: Buffer) => bad.writeFloatLE(Number.NaN, 16),
    (bad: Buffer) => bad.writeFloatLE(1.1, 16),
  ]) {
    const f = fixture();
    f.parser.push(header());
    const bad = frame();
    mutate(bad);
    assert.throws(() => f.parser.push(bad), /GUL_AUDIO_PROTOCOL/);
    assert.equal(f.frames.length, 0);
  }
});
test('native PCM rejects reordered packets and permits only explicitly marked discontinuities', () => {
  const f = fixture();
  f.parser.push(Buffer.concat([header(), frame(), frame(3, 1), frame(4)]));
  assert.equal(f.frames.length, 3);
  assert.throws(() => f.parser.push(frame(3, 1)), /GUL_AUDIO_PROTOCOL/);
  const g = fixture();
  g.parser.push(Buffer.concat([header(), frame()]));
  assert.throws(() => g.parser.push(frame(2)), /GUL_AUDIO_PROTOCOL/);
});
test('native PCM bounds untrusted pipe chunks and stops accepting data after close', () => {
  const f = fixture();
  assert.throws(() => f.parser.push(Buffer.alloc(65537)), /GUL_AUDIO_PROTOCOL/);
  const g = fixture();
  g.parser.close();
  assert.throws(() => g.parser.push(header()), /GUL_AUDIO_PROTOCOL/);
});
