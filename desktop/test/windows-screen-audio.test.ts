import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import WebSocket from 'ws';
import { DisplayCaptureConsent } from '../src/main/capture-consent.ts';
import { WindowsScreenAudio } from '../src/main/windows-screen-audio.ts';
import type { AudioHelperProcess } from '../src/main/screen-audio.ts';

class Child extends EventEmitter implements AudioHelperProcess {
  readonly stdin = new PassThrough();
  readonly stdout = new PassThrough();
  exitCode: number | null = null;
  signalCode: NodeJS.Signals | null = null;
  readonly killed: string[] = [];
  constructor() {
    super();
    this.stdin.on('finish', () => this.exit(0));
  }
  exit(code: number) {
    if (this.exitCode !== null) return;
    this.exitCode = code;
    this.emit('exit', code);
    this.stdout.end();
  }
  kill(signal = 'SIGTERM') {
    this.killed.push(signal);
    this.exit(1);
    return true;
  }
}
function preamble() {
  const data = Buffer.alloc(24);
  data.write('GULAUD1\0');
  [48000, 2, 1, 480].forEach((value, index) => data.writeUInt32LE(value, 8 + index * 4));
  return data;
}
function packet(sequence = 0) {
  const data = Buffer.alloc(24);
  data.writeUInt32LE(0x314c5547, 0);
  data.writeUInt32LE(sequence, 4);
  data.writeUInt32LE(1, 8);
  data.writeFloatLE(0.2, 16);
  data.writeFloatLE(-0.3, 20);
  return data;
}
const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 5));
function fixture(supported = true) {
  const consent = new DisplayCaptureConsent();
  let valid = true;
  const children: { args: readonly string[]; child: Child }[] = [];
  const ended: string[] = [];
  const audio = new WindowsScreenAudio({
    executable: '/trusted/gul-audio.exe',
    platform: 'win32',
    consent,
    checkExecutable: async () => true,
    nonce: (bytes) => 'a'.repeat(bytes * 2),
    startupTimeoutMs: 100,
    probeTimeoutMs: 100,
    stopTimeoutMs: 5,
    killGraceTimeoutMs: 20,
    pollIntervalMs: 5,
    onEnded: (id) => ended.push(id),
    spawn: (_file, args, options) => {
      assert.equal(options.shell, false);
      assert.equal(options.windowsHide, true);
      assert.deepEqual(options.stdio, ['pipe', 'pipe', 'ignore']);
      assert.deepEqual(args, [args[0]], 'No renderer supplied PID or capture endpoint');
      const child = new Child();
      children.push({ args, child });
      if (args[0] === '--probe')
        queueMicrotask(() => {
          child.stdout.write(supported ? 'SUPPORTED\n' : 'UNSUPPORTED\n');
          child.exit(supported ? 0 : 1);
        });
      return child;
    },
  });
  return {
    audio,
    children,
    ended,
    approve: () => consent.accept(() => valid, true, true),
    revoke: () => {
      valid = false;
    },
  };
}
async function start(f: ReturnType<typeof fixture>) {
  f.approve();
  const pending = f.audio.start();
  for (let i = 0; i < 30 && !f.children.some(({ args }) => args[0] === '--capture'); ++i) await tick();
  const child = f.children.find(({ args }) => args[0] === '--capture')!.child;
  child.stdout.write(preamble());
  return { lease: await pending, child };
}
test('Windows capability is an actual EXCLUDE initialization probe, never an OS version guess', async () => {
  const f = fixture();
  assert.equal(await f.audio.available(), true);
  assert.equal(await f.audio.available(), true);
  assert.equal(f.children.length, 1);
  assert.deepEqual(f.children[0].args, ['--probe']);
  await assert.rejects(f.audio.start(), /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  assert.equal(f.children.length, 1);
  await f.audio.close();
  const g = fixture(false);
  g.approve();
  assert.equal(await g.audio.available(), false);
  await assert.rejects(g.audio.start(), /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  assert.ok(g.children.every(({ args }) => args[0] === '--probe'));
  await g.audio.close();
});
test('one accepted display lease streams real validated stereo PCM over a private origin-bound WebSocket', async () => {
  const f = fixture();
  const { lease, child } = await start(f);
  assert.equal(lease.leaseId, 'a'.repeat(32));
  const url = new URL(lease.url);
  assert.equal(url.hostname, '127.0.0.1');
  assert.match(url.pathname, /^\/[a-f0-9]{48}$/u);
  assert.equal(url.search, '');
  child.stdout.write(packet()); // No pre-subscription backlog.
  const ws = new WebSocket(lease.url, { origin: 'gul://app' });
  await new Promise<void>((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });
  const received = new Promise<Buffer>((resolve) => ws.once('message', (data) => resolve(data as Buffer)));
  child.stdout.write(packet(1));
  assert.deepEqual(await received, packet(1));
  await f.audio.stop('b'.repeat(32));
  assert.equal(child.exitCode, null);
  const closed = new Promise<void>((resolve) => ws.once('close', () => resolve()));
  await f.audio.stop(lease.leaseId);
  await closed;
  assert.equal(child.exitCode, 0);
  assert.deepEqual(f.ended, []);
  await f.audio.close();
});
test('foreign origin, wrong nonce and reused connections never gain capture access', async () => {
  const f = fixture();
  const { lease } = await start(f);
  for (const [url, origin] of [
    [lease.url, 'https://foreign.invalid'],
    [lease.url + '?token=x', 'gul://app'],
    [lease.url.replace(/a{48}$/u, 'b'.repeat(48)), 'gul://app'],
  ]) {
    const ws = new WebSocket(url, { origin });
    await new Promise<void>((resolve) => ws.once('error', () => resolve()));
  }
  const ws = new WebSocket(lease.url, { origin: 'gul://app' });
  await new Promise<void>((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });
  const other = new WebSocket(lease.url, { origin: 'gul://app' });
  await new Promise<void>((resolve) => other.once('error', () => resolve()));
  await f.audio.close();
});
test('revoked channel consent fences late native startup and closes the process before reuse', async () => {
  const f = fixture();
  f.approve();
  const pending = f.audio.start();
  for (let i = 0; i < 30 && !f.children.some(({ args }) => args[0] === '--capture'); ++i) await tick();
  const child = f.children.find(({ args }) => args[0] === '--capture')!.child;
  f.revoke();
  child.stdout.write(preamble());
  await assert.rejects(pending, /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  await f.audio.close();
  assert.equal(child.exitCode, 0);
});
test('invalid helper PCM and incoming client data fail closed and report only the opaque lease', async () => {
  for (const invalidPcm of [true, false]) {
    const f = fixture();
    const { lease, child } = await start(f);
    const ws = new WebSocket(lease.url, { origin: 'gul://app' });
    await new Promise<void>((resolve, reject) => {
      ws.once('open', resolve);
      ws.once('error', reject);
    });
    const closed = new Promise<void>((resolve) => ws.once('close', () => resolve()));
    if (invalidPcm) {
      const bad = packet();
      bad.writeFloatLE(Number.NaN, 16);
      child.stdout.write(bad);
    } else ws.send('Not a capture control API');
    await closed;
    await f.audio.close();
    assert.deepEqual(f.ended, [lease.leaseId]);
    assert.equal(child.exitCode, 0);
  }
});

test('shutdown during the executable check cannot launch a late capability probe', async () => {
  let checked!: (value: boolean) => void;
  let launches = 0;
  const audio = new WindowsScreenAudio({
    executable: '/trusted/gul-audio.exe',
    platform: 'win32',
    consent: new DisplayCaptureConsent(),
    checkExecutable: () =>
      new Promise((resolve) => {
        checked = resolve;
      }),
    spawn: () => {
      ++launches;
      throw new Error('Unexpected late spawn');
    },
  });
  const pending = audio.available();
  await tick();
  await audio.close();
  checked(true);
  assert.equal(await pending, false);
  assert.equal(launches, 0);
});

test('a bounded native startup timeout exits the helper and leaves no live WebSocket listener', async () => {
  const f = fixture();
  f.approve();
  await assert.rejects(f.audio.start(), /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  await f.audio.close();
  const child = f.children.find(({ args }) => args[0] === '--capture')!.child;
  assert.equal(child.exitCode, 0);
  assert.deepEqual(f.ended, []);
});

test('an unread receiver cannot accumulate unbounded capture PCM', async () => {
  const f = fixture();
  const { lease, child } = await start(f);
  const ws = new WebSocket(lease.url, { origin: 'gul://app' });
  await new Promise<void>((resolve, reject) => {
    ws.once('open', resolve);
    ws.once('error', reject);
  });
  ws.pause();
  const large = Buffer.alloc(16 + 480 * 8);
  large.writeUInt32LE(0x314c5547, 0);
  large.writeUInt32LE(480, 8);
  for (let i = 0; i < 10000 && child.exitCode === null; ++i) {
    large.writeUInt32LE(i, 4);
    child.stdout.write(large);
  }
  ws.resume();
  await f.audio.close();
  assert.deepEqual(f.ended, [lease.leaseId]);
  assert.equal(child.exitCode, 0);
});

test('the renderer network allowlist grants only the exact live consent-bound audio lease URL', async () => {
  const f = fixture();
  assert.equal(f.audio.networkAllowed('ws://127.0.0.1:1/' + 'a'.repeat(48)), false);
  const { lease } = await start(f);
  assert.equal(f.audio.networkAllowed(lease.url), true);
  for (const url of [
    lease.url + '?x=1',
    lease.url + '#x',
    lease.url.replace('ws:', 'http:'),
    lease.url.replace('127.0.0.1', 'localhost'),
    lease.url.replace(/a{48}$/u, 'b'.repeat(48)),
  ])
    assert.equal(f.audio.networkAllowed(url), false);
  f.revoke();
  assert.equal(f.audio.networkAllowed(lease.url), false);
  await f.audio.close();
  assert.equal(f.audio.networkAllowed(lease.url), false);
});

test('capability probing drains stdout even when process exit arrives before its final pipe bytes', async () => {
  const child = new Child();
  const audio = new WindowsScreenAudio({
    executable: '/trusted/gul-audio.exe',
    platform: 'win32',
    consent: new DisplayCaptureConsent(),
    checkExecutable: async () => true,
    probeTimeoutMs: 100,
    spawn: () => {
      queueMicrotask(() => {
        child.exitCode = 0;
        child.emit('exit', 0);
        setTimeout(() => {
          child.stdout.write('SUPPORTED\n');
          child.stdout.end();
        }, 5);
      });
      return child;
    },
  });
  assert.equal(await audio.available(), true);
  await audio.close();
});

test('non-ASCII native output cannot impersonate the supported capability signature', async () => {
  const audio = new WindowsScreenAudio({
    executable: '/trusted/gul-audio.exe',
    platform: 'win32',
    consent: new DisplayCaptureConsent(),
    checkExecutable: async () => true,
    spawn: () => {
      const child = new Child();
      queueMicrotask(() => {
        const forged = Buffer.from('SUPPORTED\n');
        forged[0] |= 0x80;
        child.stdout.write(forged);
        child.exit(0);
      });
      return child;
    },
  });
  assert.equal(await audio.available(), false);
  await audio.close();
});
