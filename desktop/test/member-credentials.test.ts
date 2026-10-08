import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomBytes } from 'node:crypto';
import {
  MemberCredentialStore,
  parseMemberKey,
  createMemberCredential,
} from '../src/main/member-credentials.ts';

const address = 'livekit+vless://fixture.invalid';
const key = Object.freeze({
  format: 'gul-member-key-v1',
  serverId: 'a'.repeat(32),
  memberId: 'b'.repeat(32),
  credential: Buffer.alloc(32, 1).toString('base64url'),
});
const member = { id: key.memberId, role: 'owner' as const };

async function fixture(t: test.TestContext, platform = 'darwin') {
  const directory = await mkdtemp(join(tmpdir(), 'gul-member-store-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = join(directory, 'members.json');
  const values = new Map<string, string>();
  let available = true,
    backend = 'gnome_libsecret',
    broken = false;
  const safeStorage = {
    isEncryptionAvailable: () => available,
    getSelectedStorageBackend: () => backend,
    encryptString: (plain: string) => {
      if (broken) throw Error('private key error');
      const cipher = randomBytes(32);
      values.set(cipher.toString('base64'), plain);
      return cipher;
    },
    decryptString: (cipher: Buffer) => {
      if (broken) throw Error('private key error');
      const plain = values.get(cipher.toString('base64'));
      if (!plain) throw Error('private key error');
      return plain;
    },
  };
  const options = { file, safeStorage, platform };
  const store = new MemberCredentialStore(options);
  await store.load();
  return {
    file,
    store,
    reopen: async () => {
      const value = new MemberCredentialStore(options);
      await value.load();
      return value;
    },
    unavailable: () => {
      available = false;
    },
    basic: () => {
      backend = 'basic_text';
    },
    broken: () => {
      broken = true;
    },
    values,
  };
}

test('bootstrap keys use a strict bounded schema and canonical independently generated 256-bit credentials', () => {
  assert.deepEqual(parseMemberKey(key), key);
  assert.equal(Object.isFrozen(parseMemberKey(key)), true);
  const a = createMemberCredential(),
    b = createMemberCredential();
  assert.equal(Buffer.from(a, 'base64url').length, 32);
  assert.notEqual(a, b);
  for (const value of [
    null,
    [],
    { ...key, format: 'unknown' },
    { ...key, serverId: 'x'.repeat(32) },
    { ...key, memberId: key.memberId.toUpperCase() },
    { ...key, credential: 'A'.repeat(42) + 'B' },
    { ...key, credential: 12 },
    { ...key, extra: 'private' },
    { ...key, credential: key.credential + '=' },
  ])
    assert.throws(() => parseMemberKey(value), /GUL_INPUT_INVALID/u);
});

test('an imported key stays ephemeral until server confirmation and never enters public metadata', async (t) => {
  const f = await fixture(t);
  f.store.stage(address, key, true);
  const resolved = f.store.resolve(address);
  assert.equal(resolved.kind, 'ready');
  assert.equal(JSON.stringify(f.store.describe(address)).includes(key.credential), false);
  assert.equal((await f.reopen()).resolve(address).kind, 'missing');
  await assert.rejects(f.store.confirm(address, 'c'.repeat(32), member), /GUL_MEMBER_MISMATCH/u);
  await assert.rejects(
    f.store.confirm(address, key.serverId, { id: 'c'.repeat(32), role: 'owner' }),
    /GUL_MEMBER_MISMATCH/u,
  );
  await assert.rejects(
    f.store.confirm(address, key.serverId, { id: null, role: 'guest' }),
    /GUL_MEMBER_MISMATCH/u,
  );
  assert.equal((await f.reopen()).resolve(address).kind, 'missing');
  const saved = await f.store.confirm(address, key.serverId, member);
  assert.equal(saved.state, 'saved');
  assert.equal(saved.usable, true);
  const document = await readFile(f.file, 'utf8');
  assert.equal(document.includes(key.credential), false);
  assert.equal(document.includes('fixture-only-password'), false);
  const restored = await f.reopen();
  const ready = restored.resolve(address);
  assert.equal(ready.kind, 'ready');
  if (ready.kind === 'ready') assert.deepEqual(ready.key, key);
  assert.equal(restored.resolve(address + '/other').kind, 'missing');
});

test('identity consent is independent from transport-password storage and removal survives restart', async (t) => {
  const f = await fixture(t);
  f.store.stage(address, key, false);
  assert.equal((await f.store.confirm(address, key.serverId, member)).state, 'loaded');
  assert.equal((await f.reopen()).resolve(address).kind, 'missing');
  f.store.stage(address, key, true);
  await f.store.confirm(address, key.serverId, member);
  f.store.stage(address, key, false);
  await f.store.confirm(address, key.serverId, member);
  assert.equal((await f.reopen()).resolve(address).kind, 'missing');
  f.store.stage(address, key, true);
  await f.store.confirm(address, key.serverId, member);
  assert.equal((await f.store.forget(address)).persisted, true);
  assert.equal(f.store.resolve(address).kind, 'missing');
  assert.equal((await f.reopen()).resolve(address).kind, 'missing');
});

test('Linux basic_text and unavailable encryption never persist keys or silently drop loaded ownership', async (t) => {
  const f = await fixture(t, 'linux');
  f.basic();
  f.store.stage(address, key, true);
  const result = await f.store.confirm(address, key.serverId, member);
  assert.equal(result.usable, true);
  assert.equal(result.saveError, 'unavailable');
  assert.equal(f.store.resolve(address).kind, 'ready');
  assert.equal((await f.reopen()).resolve(address).kind, 'missing');
  f.unavailable();
  assert.equal((await f.store.confirm(address, key.serverId, member)).saveError, 'unavailable');
});

test('ciphertext cannot be transplanted across profiles and decryption failures are explicit', async (t) => {
  const f = await fixture(t);
  f.store.stage(address, key, true);
  await f.store.confirm(address, key.serverId, member);
  const document = JSON.parse(await readFile(f.file, 'utf8'));
  document.identities[0].address = address + '/wrong';
  await writeFile(f.file, JSON.stringify(document));
  const copied = await f.reopen();
  assert.equal(copied.resolve(address + '/wrong').kind, 'required');
  assert.equal(copied.describe(address + '/wrong').state, 'unreadable');
  f.broken();
  assert.equal(f.store.describe(address).usable, true);
  const loaded = await f.reopen();
  assert.equal(loaded.resolve(address + '/wrong').kind, 'required');
  assert.equal(JSON.stringify(loaded.describe(address + '/wrong')).includes('private key error'), false);
});

test('an unsuccessful private-file write cannot claim that a key was saved or forgotten', async (t) => {
  const f = await fixture(t);
  f.store.stage(address, key, true);
  await f.store.confirm(address, key.serverId, member);
  await rm(f.file);
  await writeFile(f.file, 'not-json');
  // A parent that is a file forces the atomic writer to fail, without changing user filesystem permissions.
  const blocked = new MemberCredentialStore({
    file: join(f.file, 'members.json'),
    platform: 'darwin',
    safeStorage: {
      isEncryptionAvailable: () => true,
      getSelectedStorageBackend: () => '',
      encryptString: () => Buffer.from('opaque'),
      decryptString: () => '',
    },
  });
  blocked.stage(address, key, true);
  const result = await blocked.confirm(address, key.serverId, member);
  assert.equal(result.state, 'loaded');
  assert.equal(result.saveError, 'write-failed');
  assert.equal((await blocked.forget(address)).persisted, false);
  assert.equal(blocked.resolve(address).kind, 'ready');
});

test('Linux identity queries and persistence never initialize crypto for locked, missing or unchecked native storage', async (t) => {
  const f = await fixture(t);
  f.store.stage(address, key, true);
  await f.store.confirm(address, key.serverId, member);
  for (const state of ['locked', 'missing', 'unavailable'] as const) {
    let calls = 0;
    const store = new MemberCredentialStore({
      file: f.file,
      platform: 'linux',
      passwordStorage: () => ({ provider: 'gnome', state, restartRequired: false }),
      safeStorage: {
        isEncryptionAvailable: () => {
          calls++;
          return true;
        },
        getSelectedStorageBackend: () => {
          calls++;
          return 'gnome_libsecret';
        },
        decryptString: () => {
          calls++;
          throw Error();
        },
        encryptString: () => {
          calls++;
          throw Error();
        },
      },
    });
    await store.load();
    assert.equal(store.describe(address).usable, false);
    assert.equal(store.resolve(address).kind, 'required');
    store.stage(address, key, true);
    assert.equal((await store.confirm(address, key.serverId, member)).saveError, 'unavailable');
    assert.equal(calls, 0);
  }
});
