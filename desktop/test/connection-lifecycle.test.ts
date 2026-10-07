import assert from 'node:assert/strict';
import test from 'node:test';
import { ConnectionLifecycle } from '../src/renderer/connection-lifecycle.ts';

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

test('cancel during media teardown prevents a late login or saved-password write', async () => {
  const lifecycle = new ConnectionLifecycle();
  const leaving = deferred();
  const calls: string[] = [];
  const operation = lifecycle.begin()!;
  const connecting = (async () => {
    if (!(await lifecycle.step(operation, () => leaving.promise)).accepted) return;
    await lifecycle.step(operation, async () => {
      calls.push('login-and-remember');
    });
  })();
  const cancelled = lifecycle.invalidate();
  const cancellation = lifecycle.cleanup(
    cancelled,
    () => leaving.promise,
    async () => {
      calls.push('disconnect');
    },
  );
  assert.deepEqual(calls, ['disconnect']);
  assert.equal(lifecycle.begin(), null);
  leaving.resolve();
  await Promise.all([connecting, cancellation]);
  assert.deepEqual(calls, ['disconnect']);
  assert.equal(lifecycle.finish(operation), false);
  assert.equal(lifecycle.finish(cancelled), true);
});

test('disconnect dispatches immediately and a new login waits for both broker and media cleanup', async () => {
  const lifecycle = new ConnectionLifecycle();
  const media = deferred();
  const broker = deferred();
  const calls: string[] = [];
  const old = lifecycle.begin()!;
  const current = lifecycle.invalidate();
  const cleanup = lifecycle.cleanup(
    current,
    () => {
      calls.push('media');
      return media.promise;
    },
    () => {
      calls.push('broker');
      return broker.promise;
    },
  );
  assert.deepEqual(calls, ['broker', 'media']);
  assert.equal(lifecycle.begin(), null);
  media.resolve();
  await Promise.resolve();
  assert.equal(lifecycle.begin(), null);
  broker.resolve();
  assert.deepEqual(await cleanup, { current: true });
  assert.equal(lifecycle.finish(current), true);
  const next = lifecycle.begin()!;
  assert.notEqual(next, old);
  assert.equal(lifecycle.current(next), true);
  await lifecycle.step(next, async () => {
    calls.push('new-login');
  });
  assert.deepEqual(calls, ['broker', 'media', 'new-login']);
});

test('an old failed login cannot dispatch cleanup or replace the new session', async () => {
  const lifecycle = new ConnectionLifecycle();
  const login = deferred<string>();
  const old = lifecycle.begin()!;
  const connecting = lifecycle.step(old, () => login.promise);
  lifecycle.invalidate();
  const next = lifecycle.begin()!;
  let disconnected = 0;
  login.reject(new Error('late old login failure'));
  assert.deepEqual(await connecting, { accepted: false });
  assert.deepEqual(
    await lifecycle.cleanup(
      old,
      async () => {},
      async () => {
        disconnected++;
      },
    ),
    { current: false },
  );
  assert.equal(disconnected, 0);
  assert.equal(lifecycle.current(next), true);
  assert.equal(lifecycle.finish(old), false);
});

test('late catch cleanup cannot change a newer cancellation state and keeps errors out of stale UI', async () => {
  const lifecycle = new ConnectionLifecycle();
  const old = lifecycle.begin()!;
  const closing = deferred();
  const oldCleanup = lifecycle.cleanup(
    old,
    () => closing.promise,
    async () => {},
  );
  const latest = lifecycle.invalidate();
  const latestCleanup = lifecycle.cleanup(
    latest,
    async () => {},
    async () => {},
  );
  assert.deepEqual(await latestCleanup, { current: true });
  assert.equal(lifecycle.begin(), null);
  closing.reject(new Error('old close failure'));
  const result = await oldCleanup;
  assert.equal(result.current, false);
  assert.equal('failure' in result, false);
  assert.equal(lifecycle.finish(latest), true);
  assert.notEqual(lifecycle.begin(), null);
});

test('current failures remain reviewable, synchronous teardown failures cannot skip broker disconnect', async () => {
  const lifecycle = new ConnectionLifecycle();
  const current = lifecycle.begin()!;
  await assert.rejects(
    lifecycle.step(current, async () => {
      throw new Error('current failure');
    }),
    /current failure/u,
  );
  let disconnected = false;
  const failure = new Error('media close failed');
  const result = await lifecycle.cleanup(
    current,
    () => {
      throw failure;
    },
    async () => {
      disconnected = true;
    },
  );
  assert.equal(disconnected, true);
  assert.equal(result.failure, failure);
  assert.equal(result.current, true);
  assert.equal(lifecycle.begin(), null);
  lifecycle.finish(current);
  assert.notEqual(lifecycle.begin(), null);
});
