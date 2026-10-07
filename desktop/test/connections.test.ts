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
      return { passwordSaved: Boolean(value.password), persisted: true, storage: 'protected' as const };
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
});
