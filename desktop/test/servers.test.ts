import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile, stat, symlink } from 'node:fs/promises';
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
  assert.equal(Object.hasOwn(rows[0], 'password'), false);
  assert.ok(Object.isFrozen(rows));
  assert.ok(Object.isFrozen(rows[0]));
  assert.equal((await readFile(file, 'utf8')).includes(input.password), false);
  if (process.platform !== 'win32') assert.equal((await stat(file)).mode & 0o777, 0o600);
  const reopened = new SavedServerStore({ file, safeStorage: secure(), platform: 'linux' });
  await reopened.load();
  assert.deepEqual(reopened.resolve(address), { kind: 'ready', input });
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
  assert.equal((await readFile(file, 'utf8')).includes('password'), false);
  assert.deepEqual(store.resolve(address), {
    kind: 'password-required',
    address,
    username: input.username,
    reason: 'unavailable',
  });
});

test('a locked or corrupt keyring falls back to manual input without exposing errors', async (t) => {
  const adapter = secure();
  const { store } = await fixture(t, adapter);
  await store.remember(input);
  adapter.decryptString = () => {
    throw Error('Password and path must remain private');
  };
  assert.equal(store.list()[0].hasPassword, false);
  assert.deepEqual(store.resolve(address), {
    kind: 'password-required',
    address,
    username: input.username,
    reason: 'locked',
  });
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
  assert.equal(store.list().length, 1);
  assert.equal((await store.forget(address)).persisted, false);
  assert.deepEqual(store.list(), []);
});
