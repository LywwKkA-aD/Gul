import test from 'node:test';
import assert from 'node:assert/strict';
import { windowsAudioLease } from '../src/renderer/media/windows-screen-audio.ts';

const lease = { leaseId: 'a'.repeat(32), url: `ws://127.0.0.1:12345/${'b'.repeat(48)}` };
test('Windows audio accepts only the owned fixed loopback WebSocket format', () => {
  assert.equal(windowsAudioLease(lease), true);
  for (const url of [
    `wss://127.0.0.1:12345/${'b'.repeat(48)}`,
    `ws://localhost:12345/${'b'.repeat(48)}`,
    `ws://127.0.0.1:12345/${'b'.repeat(48)}?extra=1`,
    `ws://127.0.0.1:12345/${'b'.repeat(48)}#extra`,
    `ws://user@127.0.0.1:12345/${'b'.repeat(48)}`,
    'ws://127.0.0.1:12345/short',
    `ws://127.0.0.1/${'b'.repeat(48)}`,
  ])
    assert.equal(windowsAudioLease({ ...lease, url }), false);
  assert.equal(windowsAudioLease({ ...lease, leaseId: 'arbitrary' }), false);
  assert.equal(windowsAudioLease({ ...lease, deviceLabel: 'microphone' }), false);
  assert.equal(windowsAudioLease(null), false);
});
