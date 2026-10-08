import test from 'node:test';
import assert from 'node:assert/strict';
import { ConnectionManager } from '../src/main/connections.ts';
import type { ConnectInput, MediaSession } from '../src/shared/contracts.ts';

const input = {
  address: 'livekit+vless://server.test',
  username: 'Tester',
  password: 'synthetic-private-value',
};
const session = { epoch: 1 } as MediaSession;
function fixture() {
  let epoch: number | null = 1;
  const saved: ConnectInput[] = [];
  const connected: ConnectInput[] = [];
  const authority = {
    connect: async (value: ConnectInput) => {
      connected.push(value);
      return session;
    },
    disconnect: async () => {
      epoch = null;
    },
    mediaEpoch: () => epoch,
  };
  const store = {
    remember: async (value: ConnectInput) => {
      saved.push(value);
      return {
        passwordSaved: Boolean(value.password),
        persisted: true,
        storage: 'protected' as const,
        status: value.password ? ('saved' as const) : ('not-requested' as const),
      };
    },
    resolve: (_address: string) => ({ kind: 'ready' as const, input }),
  };
  return { authority, store, saved, connected, manager: new ConnectionManager(authority, store) };
}

test('password persistence requires explicit consent after an accepted login', async () => {
  const f = fixture();
  await f.manager.connect({ input, rememberPassword: false });
  assert.equal(f.connected[0].password, input.password);
  assert.equal(f.saved[0].password, '');
  await f.manager.connect({ input, rememberPassword: true });
  assert.equal(f.saved[1].password, input.password);
});

test('failed or canceled login never remembers credentials', async () => {
  const f = fixture();
  f.authority.connect = async () => {
    throw Error('GUL_CONNECT_FAILED');
  };
  await assert.rejects(f.manager.connect({ input, rememberPassword: true }));
  assert.equal(f.saved.length, 0);
  let finish!: (value: MediaSession) => void;
  f.authority.connect = () =>
    new Promise<MediaSession>((resolve) => {
      finish = resolve;
    });
  const connecting = f.manager.connect({ input, rememberPassword: true });
  await f.manager.disconnect();
  finish(session);
  await assert.rejects(connecting, /GUL_SESSION_STALE/u);
  assert.equal(f.saved.length, 0);
});

test('saved connection resolves the password only in main and allows a new nickname', async () => {
  const f = fixture();
  const result = await f.manager.connectSaved({ address: input.address, username: 'New nickname' });
  assert.equal(result, session);
  assert.deepEqual(f.connected[0], { ...input, username: 'New nickname' });
  assert.equal(Object.hasOwn(result, 'password'), false);
});

test('unavailable saved password and untrusted extra fields fail closed', async () => {
  const f = fixture();
  f.store.resolve = () => ({ kind: 'missing' }) as never;
  await assert.rejects(
    f.manager.connectSaved({ address: input.address, username: input.username }),
    /GUL_SAVED_PASSWORD_REQUIRED/u,
  );
  await assert.rejects(f.manager.connect({ input, rememberPassword: 'true' }), /GUL_INPUT_INVALID/u);
  await assert.rejects(
    f.manager.connect({ input, rememberPassword: true, command: 'untrusted' }),
    /GUL_INPUT_INVALID/u,
  );
  assert.equal(f.connected.length, 0);
});

test('storage refusal does not break an already accepted connection', async () => {
  const f = fixture();
  f.store.remember = async () => {
    throw Error('private failure');
  };
  assert.equal(await f.manager.connect({ input, rememberPassword: true }), session);
  assert.deepEqual(f.manager.lastSave(), {
    address: input.address,
    status: 'write-failed',
    persisted: false,
  });
});

test('saved-password opt-out uses the credential once and removes it only after an accepted login', async () => {
  const f = fixture();
  await f.manager.connectSaved({ address: input.address, username: input.username, rememberPassword: false });
  assert.equal(f.connected[0].password, input.password);
  assert.equal(f.saved[0].password, '');
  assert.equal(f.manager.lastSave()?.status, 'not-requested');
  f.authority.connect = async () => {
    throw Error('GUL_CONNECT_FAILED');
  };
  await assert.rejects(
    f.manager.connectSaved({ address: input.address, username: input.username, rememberPassword: false }),
  );
  assert.equal(f.saved.length, 1);
  await assert.rejects(
    f.manager.connectSaved({ address: input.address, username: input.username, rememberPassword: 'false' }),
    /GUL_INPUT_INVALID/u,
  );
});

test('last save notice exposes only fixed status and remains visible after disconnect', async () => {
  const f = fixture();
  assert.equal(f.manager.lastSave(), null);
  f.store.remember = async () =>
    ({ passwordSaved: false, persisted: true, storage: 'protected', status: 'encrypt-failed' }) as never;
  await f.manager.connect({ input, rememberPassword: true });
  assert.deepEqual(f.manager.lastSave(), {
    address: input.address,
    status: 'encrypt-failed',
    persisted: true,
  });
  await f.manager.disconnect();
  assert.equal(f.manager.lastSave()?.status, 'encrypt-failed');
  assert.equal(JSON.stringify(f.manager.lastSave()).includes(input.password), false);
});

test('individual identity is independent from transport password and confirmed only after accepted bound login', async () => {
  const f = fixture(),
    key = {
      format: 'gul-member-key-v1' as const,
      serverId: 'a'.repeat(32),
      memberId: 'b'.repeat(32),
      credential: Buffer.alloc(32, 3).toString('base64url'),
    };
  const accepted = {
    ...session,
    serverId: key.serverId,
    member: { id: key.memberId, role: 'owner' as const },
  };
  let received: unknown,
    confirmed = 0;
  const authority = {
    ...f.authority,
    connect: async (_input: ConnectInput, value?: unknown) => {
      received = value;
      return accepted;
    },
  };
  const identities = {
    resolve: () => ({ kind: 'ready' as const, key, rememberIdentity: true }),
    confirm: async () => {
      confirmed++;
      return {} as never;
    },
  };
  const manager = new ConnectionManager(authority, f.store, identities);
  await manager.connect({ input, rememberPassword: false });
  assert.deepEqual(received, key);
  assert.equal(confirmed, 1);
  assert.equal(f.saved[0].password, '');
  identities.resolve = () =>
    ({ kind: 'required', reason: 'unavailable', serverId: key.serverId, memberId: key.memberId }) as never;
  await assert.rejects(manager.connect({ input, rememberPassword: true }), /GUL_MEMBER_KEY_REQUIRED/);
  assert.equal(confirmed, 1);
});
