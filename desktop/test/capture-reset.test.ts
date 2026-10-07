import assert from 'node:assert/strict';
import test from 'node:test';
import { runWithCaptureReset } from '../src/main/capture-reset.ts';
import { ConnectionManager } from '../src/main/connections.ts';
import type { MediaSession } from '../src/shared/contracts.ts';

test('pending audio shutdown never postpones connection cancellation or starts an old login later', async () => {
  let release!: () => void;
  const closing = new Promise<void>((resolve) => {
    release = resolve;
  });
  const order: string[] = [];
  const login = runWithCaptureReset(
    () => {
      order.push('reset-connect');
      return closing;
    },
    async () => {
      order.push('connect');
    },
  );
  const disconnected = runWithCaptureReset(
    async () => {
      order.push('reset-disconnect');
    },
    async () => {
      order.push('disconnect');
    },
  );
  await disconnected;
  assert.deepEqual(order, ['reset-connect', 'connect', 'reset-disconnect', 'disconnect']);
  release();
  await login;
  assert.equal(order.at(-1), 'disconnect');
});

test('both pending operations are observed when an action fails or throws synchronously', async () => {
  for (const synchronous of [false, true]) {
    let reset = 0;
    const fail = () => {
      if (synchronous) throw new Error('ACTION_FAILED');
      return Promise.reject(new Error('ACTION_FAILED'));
    };
    await assert.rejects(
      runWithCaptureReset(async () => {
        reset++;
      }, fail),
      /ACTION_FAILED/,
    );
    assert.equal(reset, 1);
  }
  await assert.rejects(
    runWithCaptureReset(
      () => {
        throw new Error('RESET_FAILED');
      },
      async () => {
        throw new Error('MUST_NOT_START');
      },
    ),
    /RESET_FAILED/,
  );
});

test('ConnectionManager rejects a canceled login while its audio reset is still pending', async () => {
  let finishLogin!: () => void;
  let finishAudio!: () => void;
  const gateway = new Promise<void>((resolve) => {
    finishLogin = resolve;
  });
  const audio = new Promise<void>((resolve) => {
    finishAudio = resolve;
  });
  let epoch: number | null = 1;
  let writes = 0;
  const manager = new ConnectionManager(
    {
      connect: async () => {
        await gateway;
        return { epoch: 1 } as MediaSession;
      },
      disconnect: async () => {
        epoch = null;
      },
      mediaEpoch: () => epoch,
    },
    {
      remember: async () => {
        writes++;
        return { passwordSaved: true, persisted: true, storage: 'protected', status: 'saved' };
      },
      resolve: () => ({ kind: 'missing' }),
    },
  );
  const attempt = runWithCaptureReset(
    () => audio,
    () =>
      manager.connect({
        input: { address: 'livekit+vless://fixture.test', username: 'Fixture', password: 'fixture-only' },
        rememberPassword: true,
      }),
  );
  const rejected = assert.rejects(attempt, /GUL_SESSION_STALE/);
  await runWithCaptureReset(
    async () => {},
    () => manager.disconnect(),
  );
  assert.equal(epoch, null);
  finishAudio();
  finishLogin();
  await rejected;
  assert.equal(writes, 0);
});
