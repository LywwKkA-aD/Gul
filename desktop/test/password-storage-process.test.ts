import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import type { ChildProcess } from 'node:child_process';
import {
  runPasswordStorageProcess,
  waitForPasswordStorageExit,
} from '../src/main/password-storage-process.ts';

test('closing during executable preflight prevents a late helper spawn', async () => {
  let ready!: (value: boolean) => void;
  const prepared = new Promise<boolean>((resolve) => {
    ready = resolve;
  });
  const abort = new AbortController();
  const children = new Set<ChildProcess>();
  let calls = 0;
  const pending = runPasswordStorageProcess('/fixture/helper', 'Gul', 'status', abort.signal, children, {
    prepare: async () => prepared,
    launch: () => {
      ++calls;
      throw Error('No helper may launch after close');
    },
  });
  abort.abort();
  await Promise.all([...children].map((child) => waitForPasswordStorageExit(child)));
  ready(true);
  assert.equal(await pending, 'unavailable');
  assert.equal(calls, 0);
  assert.equal(children.size, 0);
});

test('an aborted execution remains tracked until the actual helper exit', async () => {
  const child = Object.assign(new EventEmitter(), { exitCode: null, signalCode: null });
  const execution = Object.assign(Promise.reject(Error('AbortError')), { child });
  const children = new Set<ChildProcess>();
  await assert.rejects(
    runPasswordStorageProcess('/fixture/helper', 'Gul', 'unlock', new AbortController().signal, children, {
      prepare: async () => true,
      launch: () => execution as never,
    }),
  );
  assert.equal(children.size, 1);
  child.emit('exit', null, 'SIGTERM');
  assert.equal(children.size, 0);
  assert.equal(child.listenerCount('exit'), 0);
  assert.equal(child.listenerCount('close'), 0);
});

test('helper cleanup waits for actual exit after escalation, including signalCode exits', async () => {
  const child = Object.assign(new EventEmitter(), {
    exitCode: null,
    signalCode: null,
    kill: (_signal: string) => true,
  });
  let escalated = false;
  child.kill = (signal) => {
    assert.equal(signal, 'SIGKILL');
    escalated = true;
    return true;
  };
  let done = false;
  const closing = waitForPasswordStorageExit(child as unknown as ChildProcess, 5, 100).then(() => {
    done = true;
  });
  await new Promise((resolve) => setTimeout(resolve, 12));
  assert.equal(escalated, true);
  assert.equal(done, false);
  child.emit('exit', null, 'SIGKILL');
  await closing;
  assert.equal(done, true);
  assert.equal(child.listenerCount('exit'), 0);
  const exited = Object.assign(new EventEmitter(), { exitCode: null, signalCode: 'SIGTERM' });
  await waitForPasswordStorageExit(exited as unknown as ChildProcess, 5, 100);
  assert.equal(exited.listenerCount('exit'), 0);
});
