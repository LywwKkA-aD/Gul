import test from 'node:test';
import assert from 'node:assert/strict';
import { SessionAuthority, type SessionGateway } from '../src/main/session.ts';
import { GatewayError } from '../src/transport/errors.ts';
import type { MemberKey } from '../src/main/member-credentials.ts';
const serverId = 'a'.repeat(32),
  ownerId = 'b'.repeat(32),
  memberId = 'c'.repeat(32);
const key: MemberKey = {
  format: 'gul-member-key-v1',
  serverId,
  memberId: ownerId,
  credential: Buffer.alloc(32, 7).toString('base64url'),
};
const input = { address: 'livekit+vless://fixture.invalid', username: 'Owner', password: 'transport-only' };
const info = {
  protocolVersion: 2,
  serverId,
  channelManagement: true,
  memberAuthentication: true,
  maxChannels: 64,
};
function fixture(role: 'owner' | 'member' | 'guest' = 'owner') {
  const member = { id: role === 'guest' ? null : role === 'owner' ? ownerId : memberId, role };
  const login = {
    sessionToken: 'broker-private',
    sessionId: 7,
    identity: 'voice.7',
    name: 'Owner',
    channelId: 1,
    revision: 1,
    serverId,
    member,
    catalogVersion: 1,
    grant: {
      url: 'wss://fixture.invalid',
      token: 'media-scoped',
      identity: 'voice.7',
      ownerIdentity: 'voice.7',
      room: 'gul-channel-1',
      sessionId: 7,
      channelId: 1,
      revision: 1,
    },
  };
  const user = {
    session: 7,
    identity: 'voice.7',
    name: 'Owner',
    channelId: 1,
    isSelf: true,
    selfMute: false,
    selfDeaf: false,
  };
  const state = {
    serverId,
    member,
    catalogVersion: 2,
    selfSession: 7,
    selfChannel: 1,
    revision: 1,
    tree: {
      id: 0,
      name: 'Root',
      position: 0,
      version: 1,
      access: 'open',
      canJoin: true,
      users: [],
      children: [
        {
          id: 1,
          name: 'General',
          position: 0,
          version: 1,
          access: 'open',
          canJoin: true,
          users: [user],
          children: [],
        },
      ],
    },
  };
  const calls: { path: string; body?: unknown; token?: string }[] = [];
  let closed = 0;
  let custom: ((path: string, body: unknown) => Promise<unknown>) | undefined;
  const gateway: SessionGateway = {
    brokerOrigin: 'https://fixture.invalid',
    beginEpoch() {},
    signalURL: () => 'ws://127.0.0.1:9900/scoped',
    close: async () => {
      closed++;
    },
    request: async <T>(_method: 'GET' | 'POST', path: string, token?: string, body?: unknown) => {
      calls.push({ path, token, body });
      if (custom) return (await custom(path, body)) as T;
      return (
        path.endsWith('/info')
          ? info
          : path.endsWith('/login')
            ? login
            : path.endsWith('/members')
              ? {
                  catalogVersion: 2,
                  members: [{ id: ownerId, name: 'Owner', role: 'owner', revoked: false }],
                }
              : path.endsWith('/permissions')
                ? { channelId: 1, version: 1, access: 'open', allowedMemberIds: [] }
                : path.endsWith('/invites/create')
                  ? {
                      inviteToken: Buffer.alloc(32, 8).toString('base64url'),
                      expiresAtUnixSeconds: 2_000_000_000,
                    }
                  : state
      ) as T;
    },
  };
  return {
    gateway,
    calls,
    login,
    state,
    closed: () => closed,
    set: (fn: typeof custom) => {
      custom = fn;
    },
  };
}
test('personal key sent only after verified managed server ID; public session contains no secret', async () => {
  const f = fixture(),
    a = new SessionAuthority(async () => f.gateway);
  const session = await a.connect(input, key);
  assert.equal(f.calls[0].path, '/api/gul/info');
  assert.deepEqual(f.calls[1].body, {
    username: input.username,
    password: input.password,
    protocolVersion: 2,
    memberCredential: key.credential,
  });
  assert.equal(session.member?.role, 'owner');
  assert.equal(JSON.stringify(session).includes(key.credential), false);
  await a.disconnect();
  const wrong = fixture(),
    b = new SessionAuthority(async () => wrong.gateway);
  await assert.rejects(b.connect(input, { ...key, serverId: 'd'.repeat(32) }), /GUL_MEMBER_MISMATCH/);
  assert.equal(
    wrong.calls.some((c) => c.path.endsWith('/login')),
    false,
  );
  assert.equal(wrong.closed(), 1);
});
test('only explicit legacy 404 permits downgrade and legacy never receives personal key', async () => {
  for (const error of [
    new GatewayError('server'),
    new Error('private'),
    new GatewayError('upgrade-required'),
  ]) {
    const f = fixture();
    f.set(async () => {
      throw error;
    });
    const a = new SessionAuthority(async () => f.gateway);
    await assert.rejects(a.connect(input));
    assert.equal(f.calls.length, 1);
  }
  const f = fixture();
  f.set(async (path) => {
    if (path.endsWith('/info')) throw new GatewayError('not-found');
    return { ...f.login, serverId: undefined, member: undefined, catalogVersion: undefined };
  });
  const a = new SessionAuthority(async () => f.gateway);
  await assert.rejects(a.connect(input, key), /GUL_MEMBER_UNSUPPORTED/);
  assert.equal(f.calls.length, 1);
});
test('guest/member cannot issue owner operations even with forged renderer context', async () => {
  for (const role of ['member', 'guest'] as const) {
    const f = fixture(role),
      a = new SessionAuthority(async () => f.gateway);
    const s = await a.connect(input, role === 'member' ? { ...key, memberId } : undefined);
    await assert.rejects(a.members({ epoch: s.epoch, serverId }), /GUL_OWNER_REQUIRED/);
    await assert.rejects(
      a.createChannel({
        epoch: s.epoch,
        serverId,
        name: 'Private',
        access: 'restricted',
        allowedMemberIds: [],
        catalogVersion: 1,
      }),
      /GUL_OWNER_REQUIRED/,
    );
    assert.equal(f.calls.length, 2);
    await a.disconnect();
  }
});
test('owner mutations preserve media epoch and remove client-only context from broker body', async () => {
  const f = fixture(),
    a = new SessionAuthority(async () => f.gateway),
    s = await a.connect(input, key),
    ctx = { epoch: s.epoch, serverId };
  const catalog = await a.createChannel({
    ...ctx,
    name: 'New',
    access: 'restricted',
    allowedMemberIds: [memberId],
    catalogVersion: 1,
  });
  assert.equal(catalog.catalogVersion, 2);
  assert.equal(a.mediaEpoch(), s.epoch);
  assert.deepEqual(f.calls.at(-1)?.body, {
    name: 'New',
    access: 'restricted',
    allowedMemberIds: [memberId],
    catalogVersion: 1,
  });
  assert.equal((await a.members(ctx)).members[0].id, ownerId);
  assert.equal((await a.channelPermissions({ ...ctx, channelId: 1 })).version, 1);
  assert.equal((await a.createInvitation(ctx)).inviteToken.length, 43);
  await assert.rejects(a.deleteChannel({ ...ctx, channelId: 1, version: 1 }), /GUL_INPUT_INVALID/);
  await assert.rejects(a.members({ ...ctx, epoch: ctx.epoch + 1 }), /GUL_SESSION_STALE/);
  await a.disconnect();
});
test('late catalog mutation cannot replace a new session; sanitized policy errors remain actionable', async () => {
  const f = fixture(),
    a = new SessionAuthority(async () => f.gateway),
    s = await a.connect(input, key);
  let finish!: (v: unknown) => void;
  f.set(async (path) =>
    path.endsWith('/create')
      ? await new Promise((done) => {
          finish = done;
        })
      : f.state,
  );
  const pending = a.createChannel({
    epoch: s.epoch,
    serverId,
    name: 'New',
    access: 'open',
    allowedMemberIds: [],
    catalogVersion: 1,
  });
  await new Promise((done) => setImmediate(done));
  await a.disconnect();
  finish(f.state);
  await assert.rejects(pending, /GUL_SESSION_STALE/);
  const g = fixture(),
    b = new SessionAuthority(async () => g.gateway),
    t = await b.connect(input, key);
  g.set(async () => {
    throw new GatewayError('channel-busy');
  });
  await assert.rejects(
    b.deleteChannel({ epoch: t.epoch, serverId, channelId: 2, version: 1 }),
    /GUL_CHANNEL_BUSY/,
  );
  await b.disconnect();
});

test('revoked managed bearer closes authorized flows and returns no stale catalog', async () => {
  const f = fixture(),
    a = new SessionAuthority(async () => f.gateway),
    s = await a.connect(input, key);
  f.set(async (path) => {
    if (path.endsWith('/state')) throw new GatewayError('authentication');
    return undefined;
  });
  assert.equal(await a.state(), null);
  assert.equal(a.connected(), false);
  assert.equal(a.networkAllowed(s.grant.url), false);
  assert.equal(f.closed(), 1);
});
test('same-channel refresh cannot change the verified member identity or downgrade management', async () => {
  const f = fixture(),
    a = new SessionAuthority(async () => f.gateway);
  await a.connect(input, key);
  f.set(async (path) =>
    path.endsWith('/channel') ? { ...f.login, member: { id: memberId, role: 'member' } } : undefined,
  );
  await assert.rejects(a.channel(1), /GUL_MEMBER_MISMATCH/);
  assert.equal(a.connected(), false);
});
test('invite redemption creates a main-only credential and repeats the same credential after a lost response', async () => {
  const f = fixture('guest'),
    a = new SessionAuthority(async () => ({ ...f.gateway }));
  let attempts = 0;
  const credentials: string[] = [];
  f.set(async (path, body) => {
    if (path.endsWith('/info')) return info;
    if (path.endsWith('/redeem')) {
      credentials.push((body as any).memberCredential);
      if (++attempts === 1) throw new GatewayError('transport');
      return { serverId, member: { id: memberId, role: 'member' } };
    }
    return undefined;
  });
  const value = { input, inviteToken: Buffer.alloc(32, 4).toString('base64url'), rememberIdentity: false };
  await assert.rejects(a.redeemInvitation(value), /GUL_REDEEM_FAILED/);
  const redeemed = await a.redeemInvitation(value);
  assert.equal(redeemed.memberId, memberId);
  assert.equal(redeemed.credential, credentials[0]);
  assert.equal(credentials[0], credentials[1]);
  assert.equal(f.closed(), 2);
  assert.equal(a.idle(), true);
});
test('a delayed older catalog cannot overwrite a completed mutation in the same media epoch', async () => {
  const f = fixture(),
    a = new SessionAuthority(async () => f.gateway);
  await a.connect(input, key);
  let done!: (value: unknown) => void;
  f.set(async (path) =>
    path.endsWith('/state')
      ? await new Promise((resolve) => {
          done = resolve;
        })
      : f.state,
  );
  const polling = a.state();
  await new Promise((resolve) => setImmediate(resolve));
  await a.createChannel({
    epoch: 1,
    serverId,
    name: 'New',
    access: 'open',
    allowedMemberIds: [],
    catalogVersion: 1,
  });
  done({ ...f.state, catalogVersion: 1 });
  await assert.rejects(polling, /GUL_STATE_INVALID/);
  await a.disconnect();
});

test('a lost-response invite credential cannot be replayed when the same profile changes server identity', async () => {
  const f = fixture('guest'),
    a = new SessionAuthority(async () => ({ ...f.gateway }));
  let currentServer = serverId,
    posts = 0;
  f.set(async (path) => {
    if (path.endsWith('/info')) return { ...info, serverId: currentServer };
    if (path.endsWith('/redeem')) {
      posts++;
      throw new GatewayError('transport');
    }
    return undefined;
  });
  const value = { input, inviteToken: Buffer.alloc(32, 8).toString('base64url'), rememberIdentity: false };
  await assert.rejects(a.redeemInvitation(value), /GUL_REDEEM_FAILED/);
  currentServer = 'e'.repeat(32);
  await assert.rejects(a.redeemInvitation(value), /GUL_MEMBER_MISMATCH/);
  assert.equal(posts, 1);
});

test('explicit local identity removal clears every cached personal invite credential for the profile', async () => {
  const f = fixture('guest'),
    a = new SessionAuthority(async () => ({ ...f.gateway }));
  const credentials: string[] = [];
  f.set(async (path, body) => {
    if (path.endsWith('/info')) return info;
    if (path.endsWith('/redeem')) {
      credentials.push((body as any).memberCredential);
      return { serverId, member: { id: memberId, role: 'member' } };
    }
    return undefined;
  });
  const value = { input, inviteToken: Buffer.alloc(32, 7).toString('base64url'), rememberIdentity: false };
  await a.redeemInvitation(value);
  a.forgetIdentity(input.address);
  await a.redeemInvitation(value);
  assert.equal(credentials.length, 2);
  assert.notEqual(credentials[0], credentials[1]);
});
