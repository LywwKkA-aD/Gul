import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile, stat, symlink, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { SavedServerStore } from '../src/main/servers.ts';
import { protectedStorage, type SafeStorageAdapter } from '../src/main/storage.ts';

const address =
  'livekit+vless://server.test?security=reality&flow=none&type=tcp&sni=example.test&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=abcd';
const input = { address, username: 'Tester', password: 'synthetic-private-value' };
const secure = (backend = 'gnome_libsecret'): SafeStorageAdapter => ({
  isEncryptionAvailable: () => true,
  getSelectedStorageBackend: () => backend,
  encryptString: (plain) => Buffer.from(plain.split('').reverse().join('')),
  decryptString: (encrypted) => encrypted.toString().split('').reverse().join(''),
});
async function fixture(t: test.TestContext, safeStorage = secure(), platform = 'linux') {
  const directory = await mkdtemp(join(tmpdir(), 'gul-servers-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = join(directory, 'servers.json');
  const store = new SavedServerStore({ file, safeStorage, platform });
  await store.load();
  return { store, file, directory };
}

test('only available OS-backed encryption is reported as protected', () => {
  assert.equal(protectedStorage(secure(), 'linux'), true);
  for (const backend of ['basic_text', 'unknown', 'unexpected'])
    assert.equal(protectedStorage(secure(backend), 'linux'), false);
  assert.equal(protectedStorage({ ...secure(), isEncryptionAvailable: () => false }, 'win32'), false);
  assert.equal(
    protectedStorage(
      {
        ...secure(),
        isEncryptionAvailable: () => {
          throw Error('private');
        },
      },
      'darwin',
    ),
    false,
  );
  assert.equal(protectedStorage(secure('unknown'), 'win32'), true);
});

test('metadata stays public; encrypted passwords survive restart only inside main', async (t) => {
  const { store, file } = await fixture(t);
  assert.deepEqual(store.list(), []);
  const result = await store.remember(input);
  assert.equal(result.passwordSaved, true);
  assert.equal(result.persisted, true);
  const rows = store.list();
  assert.equal(rows[0].hasPassword, true);
  assert.equal(rows[0].rememberPassword, true);
  assert.equal(rows[0].passwordStatus, 'saved');
  assert.equal(Object.hasOwn(rows[0], 'password'), false);
  assert.ok(Object.isFrozen(rows));
  assert.ok(Object.isFrozen(rows[0]));
  assert.equal((await readFile(file, 'utf8')).includes(input.password), false);
  if (process.platform !== 'win32') assert.equal((await stat(file)).mode & 0o777, 0o600);
  const reopened = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
  await reopened.load();
  assert.deepEqual(reopened.resolve(address), { kind: 'ready', input });
  assert.equal(reopened.list()[0].rememberPassword, true);
});

test('Linux basic_text never encrypts, stores or reads a password', async (t) => {
  const adapter = {
    ...secure('basic_text'),
    encryptString: () => {
      throw Error('Must not encrypt');
    },
    decryptString: () => {
      throw Error('Must not decrypt');
    },
  };
  const { store, file } = await fixture(t, adapter);
  assert.equal((await store.remember(input)).passwordSaved, false);
  assert.equal(store.list()[0].hasPassword, false);
  assert.equal(store.list()[0].rememberPassword, true);
  assert.equal(store.list()[0].passwordStatus, 'unavailable');
  const document = JSON.parse(await readFile(file, 'utf8'));
  assert.equal(Object.hasOwn(document.servers[0], 'encryptedPassword'), false);
  assert.equal((await readFile(file, 'utf8')).includes(input.password), false);
  assert.deepEqual(store.resolve(address), {
    kind: 'password-required',
    address,
    username: input.username,
    reason: 'unavailable',
  });
});

test('a corrupt or different-key ciphertext is unreadable without falsely claiming a locked keyring', async (t) => {
  const adapter = secure();
  const { store } = await fixture(t, adapter);
  await store.remember(input);
  adapter.decryptString = () => {
    throw Error('Password and path must remain private');
  };
  assert.equal(store.list()[0].hasPassword, false);
  assert.equal(store.list()[0].rememberPassword, true);
  assert.equal(store.list()[0].passwordStatus, 'unreadable');
  assert.deepEqual(store.resolve(address), {
    kind: 'password-required',
    address,
    username: input.username,
    reason: 'unreadable',
  });
  adapter.decryptString = secure().decryptString;
  assert.equal(store.list()[0].hasPassword, true);
  assert.equal(store.list()[0].passwordStatus, 'saved');
});

test('confirmed locked native key metadata avoids synchronous storage reads and preserves the ciphertext', async (t) => {
  const { store, file } = await fixture(t);
  await store.remember(input);
  const before = await readFile(file, 'utf8');
  let calls = 0;
  let locked = true;
  const reopened = new SavedServerStore({
    file,
    platform: 'linux',
    safeStorage: {
      ...secure(),
      isEncryptionAvailable: () => {
        ++calls;
        return true;
      },
    },
    passwordStorage: () => ({
      provider: 'gnome',
      state: locked ? 'locked' : 'ready',
      restartRequired: false,
    }),
  });
  await reopened.load();
  assert.equal(reopened.list()[0].passwordStatus, 'locked');
  assert.equal(reopened.storageStatus(), 'unavailable');
  assert.equal(calls, 0);
  assert.equal(await readFile(file, 'utf8'), before);
  locked = false;
  assert.equal(reopened.list()[0].passwordStatus, 'saved');
  assert.deepEqual(reopened.resolve(address), { kind: 'ready', input });
  assert.equal(await readFile(file, 'utf8'), before);
});

test('transient encryption refusal preserves ciphertext and consent while blocking a stale previous password', async (t) => {
  const adapter = secure();
  const { store, file } = await fixture(t, adapter);
  await store.remember(input);
  const previous = JSON.parse(await readFile(file, 'utf8')).servers[0].encryptedPassword;
  adapter.encryptString = () => {
    throw Error('private keyring refusal');
  };
  const changed = { ...input, password: 'changed-private-fixture' };
  const result = await store.remember(changed);
  assert.equal(result.passwordSaved, false);
  assert.equal(result.status, 'encrypt-failed');
  assert.equal(JSON.parse(await readFile(file, 'utf8')).servers[0].encryptedPassword, previous);
  assert.equal(store.list()[0].hasPassword, false);
  assert.equal(store.list()[0].rememberPassword, true);
  assert.equal(store.list()[0].passwordStatus, 'save-failed');
  const reopened = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
  await reopened.load();
  assert.equal(reopened.list()[0].hasPassword, false);
  assert.equal(reopened.list()[0].passwordStatus, 'save-failed');
  await reopened.remember(changed);
  assert.deepEqual(reopened.resolve(address), { kind: 'ready', input: changed });
});

test('failed replacement may keep an already persisted identical password usable without pretending encryption succeeded', async (t) => {
  const adapter = secure();
  const { store, file } = await fixture(t, adapter);
  await store.remember(input);
  adapter.encryptString = () => {
    throw Error('private refusal');
  };
  const result = await store.remember(input);
  assert.equal(result.status, 'encrypt-failed');
  assert.equal(store.list()[0].hasPassword, true);
  assert.equal(store.list()[0].passwordStatus, 'save-failed');
  const reopened = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
  await reopened.load();
  assert.deepEqual(reopened.resolve(address), { kind: 'ready', input });
});

test('alpha1 encrypted profiles migrate consent and a transient unlock failure never changes disk ciphertext', async (t) => {
  const adapter = secure();
  const { file } = await fixture(t, adapter);
  await writeFile(
    file,
    JSON.stringify({
      version: 1,
      servers: [
        {
          address,
          username: input.username,
          lastUsed: 1,
          encryptedPassword: adapter
            .encryptString(JSON.stringify({ address, password: input.password }))
            .toString('base64'),
        },
      ],
    }),
  );
  const original = await readFile(file, 'utf8');
  const reopened = new SavedServerStore({ file, safeStorage: adapter, platform: 'linux' });
  await reopened.load();
  assert.equal(reopened.list()[0].rememberPassword, true);
  adapter.decryptString = () => {
    throw Error('private lock');
  };
  assert.equal(reopened.list()[0].passwordStatus, 'unreadable');
  assert.equal(await readFile(file, 'utf8'), original);
  adapter.decryptString = secure().decryptString;
  assert.equal(reopened.list()[0].hasPassword, true);
});

test('GNOME metadata does not disable valid KWallet passwords on Linux', async (t) => {
  const { file } = await fixture(t);
  const store = new SavedServerStore({
    file,
    platform: 'linux',
    safeStorage: secure('kwallet6'),
    passwordStorage: () => ({ provider: 'other', state: 'unavailable', restartRequired: false }),
  });
  await store.load();
  assert.equal((await store.remember(input)).passwordSaved, true);
  assert.equal(store.storageStatus(), 'protected');
  assert.equal(store.list()[0].passwordStatus, 'saved');
  assert.deepEqual(store.resolve(address), { kind: 'ready', input });
});

test('a failed atomic replacement keeps the committed profile and blocks using its different old password', async (t) => {
  const { store, file } = await fixture(t);
  await store.remember(input);
  await rm(file);
  await mkdir(file);
  const result = await store.remember({ ...input, username: 'Changed', password: 'new-private-fixture' });
  assert.equal(result.status, 'write-failed');
  assert.equal(result.passwordSaved, false);
  assert.equal(store.list()[0].username, input.username);
  assert.equal(store.list()[0].hasPassword, false);
  assert.equal(store.list()[0].passwordStatus, 'save-failed');
  assert.equal((await store.forget(address)).persisted, false);
  assert.equal(store.list().length, 1);
});

test('write failures on different profiles preserve each stale-password fence until that profile is committed', async (t) => {
  const { store, file } = await fixture(t);
  const second = { ...input, address: address.replace('server.test', 'other.test') };
  await store.remember(input);
  await store.remember(second);
  await rm(file);
  await mkdir(file);
  await store.remember({ ...input, password: 'new-private-a' });
  await store.remember({ ...second, password: 'new-private-b' });
  for (const row of store.list()) {
    assert.equal(row.hasPassword, false);
    assert.equal(row.passwordStatus, 'save-failed');
  }
  await rm(file, { recursive: true });
  await store.remember({ ...second, password: 'new-private-b' });
  assert.equal(store.resolve(input.address).kind, 'password-required');
  assert.equal(store.resolve(second.address).kind, 'ready');
  await store.remember({ ...input, password: 'new-private-a' });
  assert.equal(store.resolve(input.address).kind, 'ready');
});

test('eviction drops orphan failure markers without displacing fences of retained profiles', async (t) => {
  let timestamp = 0;
  const { file } = await fixture(t);
  const store = new SavedServerStore({
    file,
    safeStorage: secure(),
    platform: 'linux',
    now: () => ++timestamp,
  });
  const profile = (index: number) => ({
    ...input,
    address: address.replace('server.test', `s${index}.test`),
  });
  for (let index = 0; index < 8; index++) await store.remember(profile(index));
  await rm(file);
  await mkdir(file);
  for (const index of [1, 2, 3, 4, 5, 6, 7, 0])
    await store.remember({ ...profile(index), password: 'changed-private-fixture' });
  await rm(file, { recursive: true });
  await store.remember(profile(8));
  assert.equal(store.resolve(profile(0).address).kind, 'missing');
  await rm(file);
  await mkdir(file);
  await store.remember({ ...profile(8), password: 'changed-private-fixture' });
  assert.equal(store.list().length, 8);
  for (let index = 1; index < 9; index++)
    assert.equal(store.resolve(profile(index).address).kind, 'password-required');
});

test('ciphertext is bound to its original address and cannot authenticate another server', async (t) => {
  const { store, file } = await fixture(t);
  await store.remember(input);
  const other = address.replace('server.test', 'other.test');
  const document = JSON.parse(await readFile(file, 'utf8'));
  document.servers[0].address = other;
  await writeFile(file, JSON.stringify(document));
  const reopened = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
  await reopened.load();
  assert.equal(reopened.resolve(other).kind, 'password-required');
  assert.deepEqual(reopened.resolve(address), { kind: 'missing' });
});

test('successful reconnect updates one row; an empty password deletes stale credentials', async (t) => {
  const { store, file } = await fixture(t);
  await store.remember(input);
  await store.remember({ ...input, username: 'Changed', password: '' });
  assert.equal(store.list().length, 1);
  assert.equal(store.list()[0].username, 'Changed');
  assert.equal(store.list()[0].hasPassword, false);
  assert.equal(store.list()[0].rememberPassword, false);
  assert.equal((await readFile(file, 'utf8')).includes('encryptedPassword'), false);
  assert.equal(store.resolve(address).kind, 'password-required');
  await store.forget(address);
  assert.deepEqual(store.list(), []);
  assert.deepEqual(JSON.parse(await readFile(file, 'utf8')).servers, []);
});

test('concurrent writes remain atomic and the picker is bounded to eight newest servers', async (t) => {
  const { store, file } = await fixture(t);
  await Promise.all(
    Array.from({ length: 12 }, (_, i) =>
      store.remember({ ...input, address: address.replace('server.test', `s${i}.test`) }),
    ),
  );
  assert.equal(store.list().length, 8);
  assert.match(store.list()[0].address, /s11\.test/u);
  assert.equal(JSON.parse(await readFile(file, 'utf8')).servers.length, 8);
});

test('only valid new REALITY profiles migrate; legacy secrets and unrelated settings do not', async (t) => {
  const { store } = await fixture(t);
  await store.importLegacy({
    servers: [
      { address, username: 'Migrated', last_used: 123, password: input.password },
      { address: 'mumble://old.test', username: 'Old' },
    ],
    password: input.password,
  });
  assert.equal(store.list().length, 1);
  assert.equal(store.list()[0].username, 'Migrated');
  assert.equal(store.list()[0].hasPassword, false);
  assert.equal(store.resolve(address).kind, 'password-required');
});

test('invalid or oversized documents and symlinks are never treated as credential stores', async (t) => {
  const { file, directory } = await fixture(t);
  for (const document of ['invalid-json', JSON.stringify({ version: 99, servers: [] }), 'x'.repeat(140000)]) {
    await writeFile(file, document);
    const store = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
    await store.load();
    assert.deepEqual(store.list(), []);
  }
  await rm(file);
  const target = join(directory, 'secret-file');
  await writeFile(
    target,
    JSON.stringify({ version: 1, servers: [{ address, username: 'Private', lastUsed: 1 }] }),
  );
  await symlink(target, file);
  const store = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
  await store.load();
  assert.deepEqual(store.list(), []);
});

test('storage write failure costs persistence without rejecting the accepted connection', async (t) => {
  const { directory } = await fixture(t);
  const store = new SavedServerStore({ file: directory, safeStorage: secure(), platform: 'linux' });
  await store.load();
  const result = await store.remember(input);
  assert.equal(result.persisted, false);
  assert.equal(result.passwordSaved, false);
  assert.equal(result.status, 'write-failed');
  assert.equal(store.list().length, 0);
  assert.equal((await store.forget(address)).persisted, false);
  assert.deepEqual(store.list(), []);
});
