import assert from 'node:assert/strict';
import test from 'node:test';
import { PasswordStorage, parsePasswordStorageState } from '../src/main/password-storage.ts';
import type { SafeStorageAdapter } from '../src/main/storage.ts';

const storage = (available = true, backend = 'gnome_libsecret'): SafeStorageAdapter => ({
  isEncryptionAvailable: () => available,
  getSelectedStorageBackend: () => backend,
  encryptString: () => {
    throw Error('Unexpected secret access');
  },
  decryptString: () => {
    throw Error('Unexpected secret access');
  },
});
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

test('native protocol accepts only exact bounded nonsecret state enums', () => {
  for (const state of ['ready', 'locked', 'missing', 'cancelled', 'unavailable'] as const)
    assert.equal(parsePasswordStorageState(`GUL_PASSWORD_STORE_${state.toUpperCase()}\n`), state);
  for (const value of ['', 'READY\n', 'GUL_PASSWORD_STORE_READY\nprivate detail', 'x'.repeat(1024)])
    assert.equal(parsePasswordStorageState(value), 'unavailable');
});

test('locked and missing native status never initialize synchronous cryptography or prompt', async () => {
  let availabilityCalls = 0;
  const safeStorage = {
    ...storage(),
    isEncryptionAvailable: () => {
      ++availabilityCalls;
      return true;
    },
  };
  for (const state of ['locked', 'missing'] as const) {
    const modes: string[] = [];
    const manager = new PasswordStorage({
      platform: 'linux',
      executable: '/fixture/helper',
      applicationName: 'Gul',
      safeStorage,
      run: async (mode) => {
        modes.push(mode);
        return state;
      },
    });
    assert.deepEqual(await manager.status(), { provider: 'gnome', state, restartRequired: false });
    assert.deepEqual(modes, ['status']);
    await manager.close();
  }
  assert.equal(availabilityCalls, 0);
});

test('successful native unlock reports sticky unavailable cryptography as requiring restart', async () => {
  const modes: string[] = [];
  const manager = new PasswordStorage({
    platform: 'linux',
    executable: '/fixture/helper',
    applicationName: 'Gul',
    safeStorage: storage(false),
    run: async (mode) => {
      modes.push(mode);
      return mode === 'status' ? 'locked' : 'ready';
    },
  });
  await manager.status();
  assert.deepEqual(await manager.unlock(), { state: 'unlocked', restartRequired: true });
  assert.deepEqual(manager.getSnapshot(), { provider: 'gnome', state: 'ready', restartRequired: true });
  assert.deepEqual(modes, ['status', 'unlock']);
  await manager.close();
});

test('unlock is explicit, concurrent clicks share one prompt and cancellation retains locked state', async () => {
  const opening = deferred<'cancelled'>();
  let calls = 0;
  const manager = new PasswordStorage({
    platform: 'linux',
    executable: '/fixture/helper',
    applicationName: 'Gul',
    safeStorage: storage(),
    run: async (mode) => {
      ++calls;
      assert.equal(mode, 'unlock');
      return opening.promise;
    },
  });
  assert.equal(calls, 0);
  const one = manager.unlock();
  const two = manager.unlock();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(calls, 1);
  opening.resolve('cancelled');
  assert.deepEqual(await one, { state: 'cancelled', restartRequired: false });
  assert.deepEqual(await two, await one);
  assert.equal(manager.getSnapshot().state, 'locked');
  await manager.close();
});

test('closed manager aborts active helper and cannot accept late success', async () => {
  const pending = deferred<'ready'>();
  let signal: AbortSignal | undefined;
  const manager = new PasswordStorage({
    platform: 'linux',
    executable: '/fixture/helper',
    applicationName: 'Gul',
    safeStorage: storage(),
    run: async (_mode, value) => {
      signal = value;
      return pending.promise;
    },
  });
  const active = manager.unlock();
  await new Promise((resolve) => setImmediate(resolve));
  await manager.close();
  assert.equal(signal?.aborted, true);
  pending.resolve('ready');
  assert.deepEqual(await active, { state: 'unavailable', restartRequired: false });
  assert.equal(manager.getSnapshot().state, 'unavailable');
  assert.deepEqual(await manager.unlock(), { state: 'unavailable', restartRequired: false });
});

test('non-Linux and kwallet do not invoke GNOME helper; opening storage is fixed and failures sanitized', async () => {
  for (const [platform, backend] of [
    ['darwin', 'gnome_libsecret'],
    ['linux', 'kwallet6'],
  ] as const) {
    const manager = new PasswordStorage({
      platform,
      executable: '/fixture/helper',
      applicationName: 'Gul',
      safeStorage: storage(true, backend),
      run: async () => {
        throw Error('Must not run helper');
      },
      open: async () => {
        throw Error('Must not launch');
      },
    });
    assert.equal((await manager.status()).provider, 'other');
    assert.equal((await manager.unlock()).state, 'unavailable');
    assert.equal(await manager.open(), false);
    await manager.close();
  }
  const manager = new PasswordStorage({
    platform: 'linux',
    executable: '/fixture/helper',
    applicationName: 'Gul',
    safeStorage: storage(),
    run: async () => {
      throw Error('private password from native error');
    },
    open: async () => {
      throw Error('private OS path');
    },
  });
  assert.equal((await manager.status()).state, 'unavailable');
  assert.equal(await manager.open(), false);
  await manager.close();
});

test('basic_text is never considered protected even after native unlock', async () => {
  const manager = new PasswordStorage({
    platform: 'linux',
    executable: '/fixture/helper',
    applicationName: 'Gul',
    safeStorage: storage(true, 'basic_text'),
    run: async () => 'ready',
  });
  assert.equal((await manager.status()).restartRequired, true);
  assert.deepEqual(await manager.unlock(), { state: 'unlocked', restartRequired: true });
  await manager.close();
});
