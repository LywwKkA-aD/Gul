import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import { mkdtemp, writeFile, symlink, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { PassThrough } from 'node:stream';
import {
  executableAvailable,
  launchWindowsAudio,
  stopWindowsAudio,
} from '../src/main/windows-audio-process.ts';
import type { AudioHelperProcess } from '../src/main/screen-audio.ts';

test('bundled helper validation rejects relative paths, absent files, directories and symlinks', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-audio-process-test-'));
  try {
    const file = join(directory, 'helper.exe');
    await writeFile(file, 'fixture');
    assert.equal(await executableAvailable({ executable: file }), true);
    assert.equal(await executableAvailable({ executable: directory }), false);
    assert.equal(await executableAvailable({ executable: join(directory, 'missing.exe') }), false);
    assert.equal(await executableAvailable({ executable: 'relative.exe' }), false);
    assert.equal(
      await executableAvailable({
        executable: file,
        checkExecutable: async () => {
          throw new Error('private details');
        },
      }),
      false,
    );
    if (process.platform !== 'win32') {
      const link = join(directory, 'link.exe');
      await symlink(file, link);
      assert.equal(await executableAvailable({ executable: link }), false);
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

class DelayedChild extends EventEmitter implements AudioHelperProcess {
  readonly stdin = new PassThrough();
  readonly stdout = new PassThrough();
  exitCode: number | null = null;
  signalCode: NodeJS.Signals | null = null;
  readonly killed: string[] = [];
  kill(signal: NodeJS.Signals = 'SIGTERM') {
    this.killed.push(signal);
    setTimeout(() => {
      this.signalCode = signal;
      this.emit('exit', null, signal);
    }, 15);
    return true;
  }
}
test('shutdown waits for the actual post-SIGKILL exit and recognizes already signalled children', async () => {
  const child = new DelayedChild();
  const pending = stopWindowsAudio(child, {
    executable: '/fixture.exe',
    stopTimeoutMs: 5,
    killGraceTimeoutMs: 50,
  });
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(child.signalCode, null);
  assert.deepEqual(child.killed, ['SIGKILL']);
  await pending;
  assert.equal(child.signalCode, 'SIGKILL');
  await stopWindowsAudio(child, { executable: '/fixture.exe' });
  assert.deepEqual(child.killed, ['SIGKILL']);
});
test('failed EOF and termination still finish within the bounded kill grace', async () => {
  const child = new DelayedChild();
  child.stdin.end = () => {
    throw new Error('EOF failed');
  };
  child.kill = () => {
    throw new Error('Termination failed');
  };
  await stopWindowsAudio(child, { executable: '/fixture.exe', stopTimeoutMs: 1, killGraceTimeoutMs: 1 });
  assert.equal(child.listenerCount('exit'), 0);
});
test('the production launcher uses direct exec and only the fixed capture command', async () => {
  if (process.platform === 'win32') return;
  const child = launchWindowsAudio({ executable: '/usr/bin/false' }, '--probe');
  child.on('error', () => {});
  await stopWindowsAudio(child, { executable: '/usr/bin/false' });
  assert.notEqual(child.exitCode, null);
});
