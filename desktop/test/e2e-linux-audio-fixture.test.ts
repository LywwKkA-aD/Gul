import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import type { ChildProcess } from 'node:child_process';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { daemonEnvironment, stopFixtureProcess } from '../e2e/linux-audio-fixture.ts';

test('private Pulse daemon cannot claim the Electron portal bus or change peer environment', () => {
  const directory = join(tmpdir(), 'gul-private-audio-unit');
  const peer = Object.freeze({
    PULSE_SERVER: `unix:${join(directory, 'native')}`,
    PULSE_RUNTIME_PATH: join(directory, 'runtime'),
  });
  const browser = { DBUS_SESSION_BUS_ADDRESS: 'unix:path=/tmp/approved-desktop-bus', ...peer };
  const daemon = daemonEnvironment(peer, directory);
  assert.equal(daemon.DBUS_SESSION_BUS_ADDRESS, `unix:path=${join(directory, 'disabled-bus')}`);
  assert.equal(daemon.XDG_RUNTIME_DIR, join(directory, 'runtime'));
  assert.equal(daemon.XDG_CONFIG_HOME, join(directory, 'config'));
  assert.equal(daemon.XDG_DATA_HOME, join(directory, 'data'));
  assert.equal('HOME' in daemon, false);
  assert.equal('DBUS_SESSION_BUS_ADDRESS' in peer, false);
  assert.equal(browser.DBUS_SESSION_BUS_ADDRESS, 'unix:path=/tmp/approved-desktop-bus');
});

test('failed spawn has no PID or exit event and cannot block test fixture cleanup', async () => {
  let kills = 0;
  const child = Object.assign(new EventEmitter(), {
    pid: undefined,
    exitCode: null,
    signalCode: null,
    kill() {
      kills++;
      return false;
    },
  }) as unknown as ChildProcess;
  const result = await Promise.race([
    stopFixtureProcess(child).then(() => 'closed'),
    new Promise<string>((resolve) => setTimeout(() => resolve('blocked'), 20)),
  ]);
  assert.equal(result, 'closed');
  assert.equal(kills, 0);
});

test('fixture waits for actual owned process exit after SIGTERM and ignores an already signalled exit', async () => {
  const signals: (NodeJS.Signals | undefined)[] = [];
  const child = Object.assign(new EventEmitter(), {
    pid: 99,
    exitCode: null,
    signalCode: null as NodeJS.Signals | null,
    kill(signal?: NodeJS.Signals) {
      signals.push(signal);
      queueMicrotask(() => {
        Object.defineProperty(child, 'signalCode', { value: 'SIGTERM' });
        child.emit('exit');
      });
      return true;
    },
  }) as unknown as ChildProcess;
  await stopFixtureProcess(child);
  await stopFixtureProcess(child);
  assert.deepEqual(signals, ['SIGTERM']);
});
