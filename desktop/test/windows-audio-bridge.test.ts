import assert from 'node:assert/strict';
import test from 'node:test';
import WebSocket from 'ws';
import { WindowsAudioBridge, type WindowsAudioBridgeFailure } from '../src/main/windows-audio-bridge.ts';

async function connected(url: string): Promise<WebSocket> {
  const client = new WebSocket(url, { origin: 'gul://app' });
  await new Promise<void>((resolve, reject) => {
    client.once('open', resolve);
    client.once('error', reject);
  });
  return client;
}
async function reported(reasons: readonly WindowsAudioBridgeFailure[]): Promise<void> {
  for (let attempt = 0; attempt < 100 && reasons.length === 0; attempt++)
    await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(reasons.length, 1);
}
test('an unread local receiver reports backlog once without expanding the 120ms bridge capacity', async () => {
  const reasons: WindowsAudioBridgeFailure[] = [];
  const bridge = new WindowsAudioBridge(
    'a'.repeat(48),
    () => true,
    (reason) => reasons.push(reason),
  );
  const client = await connected(await bridge.listen());
  try {
    client.pause();
    const frame = Buffer.alloc(16 + 480 * 8);
    for (let index = 0; index < 10000 && reasons.length === 0; index++) bridge.send(frame);
    assert.deepEqual(reasons, ['backlog']);
    bridge.send(frame);
    assert.deepEqual(reasons, ['backlog']);
  } finally {
    client.resume();
    await bridge.close();
  }
});
test('consent revocation reports only its fixed reason and explicit shutdown reports no failure', async () => {
  const reasons: WindowsAudioBridgeFailure[] = [];
  let valid = true;
  const bridge = new WindowsAudioBridge(
    'a'.repeat(48),
    () => valid,
    (reason) => reasons.push(reason),
  );
  await bridge.listen();
  try {
    valid = false;
    bridge.send(Buffer.alloc(24));
    assert.deepEqual(reasons, ['invalid-consent']);
  } finally {
    await bridge.close();
  }
  const closing = new WindowsAudioBridge(
    'b'.repeat(48),
    () => true,
    (reason) => reasons.push(reason),
  );
  await connected(await closing.listen());
  await closing.close();
  assert.deepEqual(reasons, ['invalid-consent']);
});
test('receiver closure and forbidden messages stay distinguishable without including their payload', async () => {
  for (const reason of ['client-message', 'client-close'] as const) {
    const reasons: WindowsAudioBridgeFailure[] = [];
    const bridge = new WindowsAudioBridge(
      'a'.repeat(48),
      () => true,
      (value) => reasons.push(value),
    );
    const client = await connected(await bridge.listen());
    try {
      if (reason === 'client-message') client.send('private test payload');
      else client.close();
      await reported(reasons);
      assert.deepEqual(reasons, [reason]);
    } finally {
      await bridge.close();
    }
  }
});
