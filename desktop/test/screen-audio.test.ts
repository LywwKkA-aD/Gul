import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { DisplayCaptureConsent } from '../src/main/capture-consent.ts';
import { NativeScreenAudio, type AudioHelperProcess } from '../src/main/screen-audio.ts';

class Child extends EventEmitter implements AudioHelperProcess {
  readonly stdin = new PassThrough();
  readonly stdout = new PassThrough();
  exitCode: number | null = null;
  signalCode: NodeJS.Signals | null = null;
  readonly signals: string[] = [];
  readonly orderly: boolean;
  constructor(orderly = true) {
    super();
    this.orderly = orderly;
    this.stdin.on('finish', () => {
      if (this.orderly) queueMicrotask(() => this.exit(0));
    });
  }
  exit(code: number) {
    if (this.exitCode !== null || this.signalCode !== null) return;
    this.exitCode = code;
    this.emit('exit', code);
  }
  kill(signal = 'SIGTERM') {
    this.signals.push(signal);
    if (signal === 'SIGKILL' || this.orderly) this.exit(0);
    return true;
  }
}
function fixture(orderly = true, captureChild?: Child) {
  const consent = new DisplayCaptureConsent();
  const children: Child[] = [];
  const args: readonly string[][] = [];
  const calls: string[][] = args as string[][];
  const ended: string[] = [];
  let active = true;
  const manager = new NativeScreenAudio({
    executable: '/trusted/gul-audio',
    platform: 'linux',
    consent,
    checkExecutable: async () => true,
    resolveEndpoint: async () => 'unix:/fixture/pulse/native',
    nonce: () => 'a'.repeat(32),
    startupTimeoutMs: 30,
    stopTimeoutMs: 5,
    killGraceTimeoutMs: 30,
    pollIntervalMs: 5,
    onEnded: (leaseId) => ended.push(leaseId),
    spawn: (_file, arguments_, options) => {
      assert.equal(options.shell, false);
      assert.deepEqual(options.stdio, ['pipe', 'pipe', 'ignore']);
      calls.push([...arguments_]);
      const child =
        arguments_[0] === '--capture' && captureChild
          ? captureChild
          : new Child(arguments_[0] === '--cleanup' || orderly);
      children.push(child);
      if (arguments_[0] === '--cleanup') queueMicrotask(() => child.exit(0));
      return child;
    },
  });
  const approve = () => consent.accept(() => active, true, true);
  return {
    manager,
    consent,
    children,
    args,
    ended,
    approve,
    revoke: () => {
      active = false;
    },
  };
}
const tick = () => new Promise((resolve) => setTimeout(resolve, 1));

test('native audio requires accepted display consent and a bundled available executable', async () => {
  const f = fixture();
  await assert.rejects(f.manager.start(), /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  assert.equal(f.children.length, 0);
  const unsupported = new NativeScreenAudio({
    executable: '/trusted/gul-audio',
    platform: 'darwin',
    consent: f.consent,
  });
  assert.equal(await unsupported.available(), false);
  await unsupported.close();
});

test('ready audio returns only a private generated device label and one opaque lease', async () => {
  const f = fixture();
  f.approve();
  const pending = f.manager.start();
  await tick();
  f.children[0].stdout.write('REA');
  f.children[0].stdout.write('DY\n');
  const lease = await pending;
  assert.deepEqual(lease, { leaseId: 'a'.repeat(32), deviceLabel: 'Gul-Screen-Audio-' + 'a'.repeat(32) });
  await f.manager.stop('b'.repeat(32));
  assert.equal(f.children[0].exitCode, null, 'Unknown lease cannot stop another accepted capture');
  await f.manager.stop(lease.leaseId);
  assert.equal(f.children[0].exitCode, 0);
  assert.deepEqual(f.args[1], ['--cleanup', lease.leaseId]);
  await f.manager.close();
});

test('session cancellation while the helper starts stops late ready audio', async () => {
  const f = fixture();
  f.approve();
  const pending = f.manager.start();
  await tick();
  f.revoke();
  f.children[0].stdout.write('READY\n');
  await assert.rejects(pending, /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  await f.manager.close();
  assert.equal(f.children[0].exitCode, 0);
});

test('malformed helper output and startup timeout fail closed without exposing native text', async () => {
  for (const value of ['PRIVATE RAW ERROR\n', 'x'.repeat(4097), null]) {
    const f = fixture();
    f.approve();
    const pending = f.manager.start();
    await tick();
    if (value) f.children[0].stdout.write(value);
    await assert.rejects(
      pending,
      (error: unknown) => error instanceof Error && error.message === 'GUL_SCREEN_AUDIO_UNAVAILABLE',
    );
    await f.manager.close();
    assert.equal(f.children[0].exitCode, 0);
  }
});

test('running session revocation and helper exit notify only the current lease and stop capture', async () => {
  for (const reason of ['session', 'exit'] as const) {
    const f = fixture();
    f.approve();
    const pending = f.manager.start();
    await tick();
    f.children[0].stdout.write('READY\n');
    const lease = await pending;
    if (reason === 'exit') f.children[0].exit(1);
    else f.revoke();
    await new Promise((resolve) => setTimeout(resolve, 12));
    assert.deepEqual(f.ended, [lease.leaseId]);
    await f.manager.close();
  }
});

test('close fences pending startup and forces a stalled helper to die before cleanup', async () => {
  const f = fixture(false);
  f.approve();
  const pending = f.manager.start();
  const rejected = assert.rejects(pending, /GUL_SCREEN_AUDIO_UNAVAILABLE/);
  await tick();
  await f.manager.close();
  await rejected;
  assert.ok(f.children[0].signals.includes('SIGKILL'));
  assert.equal(f.args.at(-1)?.[0], '--cleanup');
});

test('cleanup waits for the actual signal exit after killing a stalled helper', async () => {
  class DelayedExit extends Child {
    override kill(signal: NodeJS.Signals = 'SIGTERM') {
      this.signals.push(signal);
      if (signal === 'SIGKILL')
        setTimeout(() => {
          this.signalCode = signal;
          this.emit('exit', null, signal);
        }, 15);
      return true;
    }
  }
  const child = new DelayedExit(false);
  const f = fixture(false, child);
  f.approve();
  const pending = f.manager.start();
  await tick();
  child.stdout.write('READY\n');
  await pending;
  const closing = f.manager.close();
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.deepEqual(child.signals, ['SIGKILL']);
  assert.equal(f.args.length, 1, 'A kill request alone must not race orphan cleanup');
  await closing;
  assert.equal(child.signalCode, 'SIGKILL');
  assert.equal(child.exitCode, null);
  assert.equal(f.args[1]?.[0], '--cleanup');
});

test('an already signalled helper is closed without another kill or exit wait', async () => {
  const f = fixture();
  f.approve();
  const pending = f.manager.start();
  await tick();
  const child = f.children[0];
  child.stdout.write('READY\n');
  await pending;
  child.signalCode = 'SIGTERM';
  child.emit('exit', null, 'SIGTERM');
  await f.manager.close();
  assert.deepEqual(child.signals, []);
  assert.equal(f.args[1]?.[0], '--cleanup');
});
