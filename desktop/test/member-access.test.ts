import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, symlink, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { MemberAccess } from '../src/main/member-access.ts';
import { MemberCredentialStore } from '../src/main/member-credentials.ts';
const address = 'livekit+vless://fixture.invalid',
  key = {
    format: 'gul-member-key-v1' as const,
    serverId: 'a'.repeat(32),
    memberId: 'b'.repeat(32),
    credential: Buffer.alloc(32, 9).toString('base64url'),
  };
const crypto = {
  isEncryptionAvailable: () => false,
  getSelectedStorageBackend: () => 'basic_text',
  encryptString: () => {
    throw Error();
  },
  decryptString: () => {
    throw Error();
  },
};
async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'gul-member-access-'));
  const store = new MemberCredentialStore({
    file: join(directory, 'members.json'),
    safeStorage: crypto,
    platform: 'linux',
  });
  await store.load();
  let epoch = 0,
    idle = true;
  const authority = {
    idle: () => idle,
    forgetIdentity: () => {},
    operationRevision: () => epoch,
    redeemInvitation: async () => {
      epoch++;
      return key;
    },
  };
  return {
    directory,
    store,
    access: new MemberAccess(authority, store),
    advance: () => {
      epoch++;
    },
    busy: () => {
      idle = false;
    },
    close: () => rm(directory, { recursive: true, force: true }),
  };
}
test('native import reads bounded regular JSON, stages only main identity and requires explicit consent', async () => {
  const f = await fixture();
  try {
    const file = join(f.directory, 'owner.json');
    await writeFile(file, JSON.stringify(key));
    const result = await f.access.import({ address, rememberIdentity: false }, async () => file);
    assert.equal(result?.state, 'loaded');
    assert.equal(JSON.stringify(result).includes(key.credential), false);
    assert.equal(await f.access.import({ address, rememberIdentity: true }, async () => null), null);
    await assert.rejects(
      f.access.import({ address, rememberIdentity: false, path: file } as never, async () => file),
      /GUL_INPUT_INVALID/,
    );
    await writeFile(file, ' '.repeat(5000));
    await assert.rejects(
      f.access.import({ address, rememberIdentity: true }, async () => file),
      /GUL_MEMBER_IMPORT_FAILED/,
    );
    await writeFile(file, JSON.stringify(key));
    const link = join(f.directory, 'link.json');
    await symlink(file, link);
    await assert.rejects(
      f.access.import({ address, rememberIdentity: true }, async () => link),
      /GUL_MEMBER_IMPORT_FAILED/,
    );
  } finally {
    await f.close();
  }
});
test('late native dialog cannot stage identity after connect or after a newer import', async () => {
  const f = await fixture();
  try {
    const file = join(f.directory, 'owner.json');
    await writeFile(file, JSON.stringify(key));
    let done!: (path: string) => void;
    const pending = f.access.import(
      { address, rememberIdentity: true },
      () =>
        new Promise((resolve) => {
          done = resolve;
        }),
    );
    f.advance();
    done(file);
    await assert.rejects(pending, /GUL_SESSION_STALE/);
    assert.equal(f.store.describe(address).state, 'none');
    f.busy();
    await assert.rejects(
      f.access.import({ address, rememberIdentity: true }, async () => file),
      /GUL_SESSION_STALE/,
    );
  } finally {
    await f.close();
  }
});
test('invite redemption saves verified identity by its own consent and never returns generated key', async () => {
  const f = await fixture();
  try {
    const result = await f.access.redeem({
      input: { address, username: 'Member', password: 'shared' },
      inviteToken: Buffer.alloc(32, 4).toString('base64url'),
      rememberIdentity: true,
    });
    assert.equal(result.usable, true);
    assert.equal(result.saveError, 'unavailable');
    assert.equal(JSON.stringify(result).includes(key.credential), false);
    await f.access.clear(address);
    assert.equal(f.store.describe(address).state, 'none');
  } finally {
    await f.close();
  }
});

test('identity consent changes main-only staged key without requiring another file import', async () => {
  const f = await fixture();
  try {
    f.store.stage(address, key, true);
    const result = f.access.consent({ address, rememberIdentity: false });
    assert.equal(result.rememberIdentity, false);
    assert.equal(result.usable, true);
    assert.equal(JSON.stringify(result).includes(key.credential), false);
    await assert.rejects(
      async () => f.access.consent({ address, rememberIdentity: 'false' } as never),
      /GUL_INPUT_INVALID/,
    );
    await assert.rejects(
      async () => f.access.consent({ address: 'livekit+vless://other.invalid', rememberIdentity: true }),
      /GUL_MEMBER_KEY_REQUIRED/,
    );
  } finally {
    await f.close();
  }
});
