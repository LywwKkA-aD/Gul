import test from 'node:test';
import assert from 'node:assert/strict';
import { SessionAuthority, type SessionGateway } from '../src/main/session.ts';

const address = 'livekit+vless://203.0.113.7?security=reality';
const info = {
  protocolVersion: 2,
  serverId: null,
  channelManagement: false,
  memberAuthentication: false,
  maxChannels: 64,
};
const input = { address, username: 'Тест', password: 'private-password' };
const base = () => ({
  sessionToken: 'private-broker-session',
  sessionId: 7,
  identity: 'voice.7',
  name: 'Тест',
  channelId: 1,
  revision: 1,
  grant: {
    url: 'wss://203.0.113.7',
    token: 'scoped-media',
    identity: 'voice.7',
    room: 'gul-channel-1',
    ownerIdentity: 'voice.7',
    sessionId: 7,
    channelId: 1,
    revision: 1,
  },
});
function fake() {
  const calls: { method: string; path: string; token?: string; body?: unknown }[] = [];
  let login = base();
  let closed = 0;
  let epochs: number[] = [];
  let delayed: Promise<unknown> | undefined;
  const gateway: SessionGateway = {
    brokerOrigin: 'https://203.0.113.7',
    request: async <T>(method: 'GET' | 'POST', path: string, token?: string, body?: unknown): Promise<T> => {
      calls.push({ method, path, token, body });
      if (path === '/api/gul/info') return info as T;
      if (path === '/api/gul/login') return structuredClone(login) as T;
      if (path === '/api/gul/screen')
        return (delayed ? await delayed : { ...login.grant, identity: 'screen.7' }) as T;
      if (path === '/api/gul/channel') {
        const channelId = (body as { channelId: number }).channelId;
        const revision = login.channelId === channelId ? login.revision : login.revision + 1;
        login = {
          ...login,
          channelId,
          revision,
          grant: {
            ...login.grant,
            token: 'fresh-media-token',
            channelId,
            revision,
            room: `gul-channel-${channelId}`,
          },
        };
        return structuredClone(login) as T;
      }
      if (path === '/api/gul/audio') return body as T;
      if (path === '/api/gul/state')
        return {
          tree: { id: 0, name: 'Root', position: 0, users: [], children: [] },
          selfSession: 7,
          selfChannel: login.channelId,
          revision: login.revision,
        } as T;
      return undefined as T;
    },
    beginEpoch: (epoch) => {
      epochs = [...epochs, epoch];
    },
    signalURL: (epoch, token) => `ws://127.0.0.1:9000/${epoch}/${token}`,
    close: async () => {
      closed++;
    },
  };
  return {
    gateway,
    calls,
    setLogin: (value: ReturnType<typeof base>) => {
      login = value;
    },
    setDelayed: (value: Promise<unknown>) => {
      delayed = value;
    },
    closed: () => closed,
    epochs: () => epochs,
  };
}

test('broker credentials stay in main; only verified scoped media enters renderer', async () => {
  const f = fake();
  const authority = new SessionAuthority(async () => f.gateway);
  const session = await authority.connect(input);
  assert.equal(session.grant.url, 'ws://127.0.0.1:9000/1/scoped-media');
  assert.equal(session.grant.identity, 'voice.7');
  assert.equal(authority.mediaEpoch(), session.epoch);
  assert.equal(JSON.stringify(session).includes('private-'), false);
  assert.equal('sessionToken' in session, false);
  await authority.state();
  assert.equal(f.calls.at(-1)?.token, 'private-broker-session');
  await authority.disconnect();
  assert.equal(f.closed(), 1);
  assert.equal(await authority.state(), null);
  assert.equal(authority.mediaEpoch(), null);
});

test('screen grant requires current channel/revision and binds all identity fields', async () => {
  const f = fake();
  const authority = new SessionAuthority(async () => f.gateway);
  const session = await authority.connect(input);
  const grant = await authority.screen({ channelId: 1, revision: 1 });
  assert.equal(grant.identity, 'screen.7');
  assert.match(grant.url, /^ws:\/\/127\.0\.0\.1:/);
  await assert.rejects(authority.screen({ channelId: 2, revision: 1 }), /GUL_SESSION_STALE/);
  const moved = await authority.channel(2);
  assert.equal(moved.epoch, session.epoch + 1);
  assert.equal(moved.grant.room, 'gul-channel-2');
  await assert.rejects(authority.screen({ channelId: 1, revision: 1 }), /GUL_SESSION_STALE/);
  await authority.disconnect();
});

test('a late screen request cannot escape after channel transition', async () => {
  const f = fake();
  const authority = new SessionAuthority(async () => f.gateway);
  await authority.connect(input);
  let resolve!: (value: unknown) => void;
  f.setDelayed(
    new Promise((done) => {
      resolve = done;
    }),
  );
  const pending = authority.screen({ channelId: 1, revision: 1 });
  await authority.channel(2);
  resolve({ ...base().grant, identity: 'screen.7' });
  await assert.rejects(pending, /GUL_SESSION_STALE/);
  await authority.disconnect();
});

test('invalid and redirected grants are rejected without forwarding private error text', async () => {
  for (const change of [
    { identity: 'screen.7' },
    { room: 'other' },
    { sessionId: 8 },
    { ownerIdentity: 'voice.8' },
    { channelId: 2 },
    { revision: 2 },
    { url: 'wss://other.example' },
    { url: 'wss://203.0.113.7/rtc' },
    { url: 'wss://203.0.113.7?secret=x' },
  ]) {
    const f = fake();
    const login = base();
    f.setLogin({ ...login, grant: { ...login.grant, ...change } });
    const authority = new SessionAuthority(async () => f.gateway);
    await assert.rejects(authority.connect(input), /GUL_GRANT_INVALID/);
    assert.equal(f.closed(), 1);
  }
  const gateway = fake().gateway;
  gateway.request = async () => {
    throw new Error('https://secret.example?token=private-password');
  };
  const authority = new SessionAuthority(async () => gateway);
  await assert.rejects(authority.connect(input), (error: Error) => error.message === 'GUL_CONNECT_FAILED');
});

test('connect replacement fences an older pending login and cleans its gateway', async () => {
  const a = fake(),
    b = fake();
  let resolve!: (value: unknown) => void;
  a.gateway.request = async <T>(_method: 'GET' | 'POST', path: string) => {
    if (path === '/api/gul/info') return info as T;
    return (await new Promise<unknown>((done) => {
      resolve = done;
    })) as T;
  };
  let attempt = 0;
  const authority = new SessionAuthority(async () => (attempt++ ? b.gateway : a.gateway));
  const first = authority.connect(input);
  await new Promise((done) => setImmediate(done));
  const second = await authority.connect(input);
  resolve(base());
  await assert.rejects(first, /GUL_SESSION_STALE/);
  assert.equal(second.epoch, 2);
  assert.equal(a.closed(), 1);
  await authority.disconnect();
});

test('invalid IPC inputs never reach transport', async () => {
  const f = fake();
  const authority = new SessionAuthority(async () => f.gateway);
  for (const value of [
    null,
    [],
    { ...input, username: '' },
    { ...input, password: 12 },
    { ...input, username: 'x\0' },
    { ...input, unexpected: true },
  ]) {
    await assert.rejects(authority.connect(value as never), /GUL_INPUT_INVALID/);
  }
  assert.equal(f.calls.length, 0);
  await authority.connect(input);
  await assert.rejects(authority.channel(9), /GUL_INPUT_INVALID/);
  await assert.rejects(authority.audio({ muted: false, deafened: 'yes' } as never), /GUL_INPUT_INVALID/);
  assert.deepEqual(await authority.audio({ muted: false, deafened: true }), { muted: true, deafened: true });
  await authority.disconnect();
});

test('failed channel closes flows once even when the gateway requires monotonic epochs', async () => {
  const f = fake();
  const original = f.gateway.request;
  let epoch = 0;
  f.gateway.beginEpoch = (next) => {
    if (next <= epoch) throw new Error('epoch must increase');
    epoch = next;
  };
  f.gateway.request = async <T>(method: 'GET' | 'POST', path: string, token?: string, body?: unknown) => {
    if (path === '/api/gul/channel') throw new Error('secret private transport failure');
    return original<T>(method, path, token, body);
  };
  const authority = new SessionAuthority(async () => f.gateway);
  const joined = await authority.connect(input);
  assert.equal(authority.networkAllowed(`${joined.grant.url}/rtc`), true);
  await assert.rejects(authority.channel(2), (error: Error) => error.message === 'GUL_CHANNEL_FAILED');
  assert.equal(f.closed(), 1);
  assert.equal(authority.networkAllowed(`${joined.grant.url}/rtc`), false);
  assert.equal(await authority.state(), null);
  assert.equal(authority.mediaEpoch(), null);
});

test('disconnect fences pending audio and channel responses', async () => {
  for (const operation of ['audio', 'channel'] as const) {
    const f = fake();
    const original = f.gateway.request;
    let resolve!: (value: unknown) => void;
    let began!: () => void;
    const started = new Promise<void>((done) => {
      began = done;
    });
    f.gateway.request = async <T>(
      method: 'GET' | 'POST',
      path: string,
      token?: string,
      body?: unknown,
    ): Promise<T> => {
      if (path === `/api/gul/${operation}`) {
        began();
        return (await new Promise<unknown>((done) => {
          resolve = done;
        })) as T;
      }
      return original<T>(method, path, token, body);
    };
    const authority = new SessionAuthority(async () => f.gateway);
    await authority.connect(input);
    const pending =
      operation === 'audio' ? authority.audio({ muted: true, deafened: false }) : authority.channel(2);
    await started;
    await authority.disconnect();
    const value = base();
    resolve(
      operation === 'audio'
        ? { muted: true, deafened: false }
        : {
            ...value,
            channelId: 2,
            revision: 2,
            grant: { ...value.grant, channelId: 2, revision: 2, room: 'gul-channel-2' },
          },
    );
    await assert.rejects(pending, /GUL_SESSION_STALE/);
    assert.equal(f.closed(), 1);
    assert.equal(authority.connected(), false);
  }
});

test('same-channel media rejoin obtains a fresh JWT without changing broker revision', async () => {
  const f = fake();
  const expired = base();
  expired.grant.token = 'expired-initial-media-token';
  f.setLogin(expired);
  const authority = new SessionAuthority(async () => f.gateway);
  const joined = await authority.connect(input);
  const again = await authority.channel(1);
  assert.equal(again.epoch, joined.epoch + 1);
  assert.equal(again.revision, joined.revision);
  assert.equal(again.sessionId, joined.sessionId);
  assert.equal(again.grant.token, 'fresh-media-token');
  assert.notEqual(again.grant.token, joined.grant.token);
  assert.equal(f.calls.filter((call) => call.path === '/api/gul/channel').length, 1);
  assert.equal(authority.networkAllowed(`${joined.grant.url}/rtc`), false);
  assert.equal(authority.networkAllowed(`${again.grant.url}/rtc`), true);
  const otherRole = await authority.screen({ channelId: 1, revision: 1 });
  assert.equal(otherRole.identity, 'screen.7');
  assert.equal(otherRole.ownerIdentity, joined.identity);
  await authority.disconnect();
});

test('same-channel refresh cannot replace the authenticated session scope', async () => {
  for (const change of [
    { sessionToken: 'foreign-broker-token' },
    { sessionId: 8, identity: 'voice.8' },
    { channelId: 2 },
    { revision: 2 },
  ]) {
    const f = fake();
    const original = f.gateway.request;
    f.gateway.request = async <T>(
      method: 'GET' | 'POST',
      path: string,
      token?: string,
      body?: unknown,
    ): Promise<T> => {
      if (path === '/api/gul/channel') {
        const value = { ...base(), ...change };
        value.grant = {
          ...value.grant,
          sessionId: value.sessionId,
          identity: value.identity,
          ownerIdentity: value.identity,
          channelId: value.channelId,
          room: `gul-channel-${value.channelId}`,
          revision: value.revision,
        };
        return value as T;
      }
      return original<T>(method, path, token, body);
    };
    const authority = new SessionAuthority(async () => f.gateway);
    await authority.connect(input);
    await assert.rejects(authority.channel(1), /GUL_GRANT_INVALID/);
    assert.equal(f.closed(), 1);
  }
});

test('queued audio from an old session cannot mute a replacement login', async () => {
  const a = fake(),
    b = fake();
  const original = a.gateway.request;
  let release!: (value: unknown) => void;
  let began!: () => void;
  const started = new Promise<void>((done) => {
    began = done;
  });
  a.gateway.request = async <T>(
    method: 'GET' | 'POST',
    path: string,
    token?: string,
    body?: unknown,
  ): Promise<T> => {
    if (path === '/api/gul/audio') {
      began();
      return (await new Promise<unknown>((done) => {
        release = done;
      })) as T;
    }
    return original<T>(method, path, token, body);
  };
  let attempt = 0;
  const authority = new SessionAuthority(async () => (attempt++ ? b.gateway : a.gateway));
  await authority.connect(input);
  const first = authority.audio({ muted: true, deafened: false });
  await started;
  const queued = authority.audio({ muted: true, deafened: true });
  await authority.connect(input);
  release({ muted: true, deafened: false });
  await assert.rejects(first, /GUL_SESSION_STALE/);
  await assert.rejects(queued, /GUL_SESSION_STALE/);
  assert.equal(
    b.calls.some((call) => call.path === '/api/gul/audio'),
    false,
  );
  await authority.disconnect();
});

test('disconnect immediately closes a pending channel gateway and rejects its late response', async () => {
  const f = fake();
  const original = f.gateway.request;
  let resolve!: (value: unknown) => void;
  let began!: () => void;
  const started = new Promise<void>((done) => {
    began = done;
  });
  f.gateway.request = async <T>(
    method: 'GET' | 'POST',
    path: string,
    token?: string,
    body?: unknown,
  ): Promise<T> => {
    if (path === '/api/gul/channel') {
      began();
      return (await new Promise<unknown>((done) => {
        resolve = done;
      })) as T;
    }
    return original<T>(method, path, token, body);
  };
  const authority = new SessionAuthority(async () => f.gateway);
  await authority.connect(input);
  const pending = authority.channel(2);
  await started;
  await authority.disconnect();
  assert.equal(f.closed(), 1);
  const value = base();
  resolve({
    ...value,
    channelId: 2,
    revision: 2,
    grant: { ...value.grant, channelId: 2, revision: 2, room: 'gul-channel-2' },
  });
  await assert.rejects(pending, /GUL_SESSION_STALE/);
});

test('disconnect waits for an unfinished gateway factory and closes its eventual process', async () => {
  const f = fake();
  let resolve!: (gateway: SessionGateway) => void;
  const authority = new SessionAuthority(
    async () =>
      await new Promise<SessionGateway>((done) => {
        resolve = done;
      }),
  );
  const connecting = authority.connect(input);
  const rejected = assert.rejects(connecting, /GUL_SESSION_STALE/);
  let done = false;
  const disconnected = authority.disconnect().then(() => {
    done = true;
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(done, false);
  resolve(f.gateway);
  await disconnected;
  await rejected;
  assert.equal(f.closed(), 1);
});

test('disconnect drains cleanup already started for a replaced session', async () => {
  const a = fake(),
    b = fake();
  const original = a.gateway.request;
  let finish!: () => void;
  const release = new Promise<void>((done) => {
    finish = done;
  });
  a.gateway.request = async <T>(
    method: 'GET' | 'POST',
    path: string,
    token?: string,
    body?: unknown,
  ): Promise<T> => {
    if (path === '/api/gul/logout') {
      await release;
      return undefined as T;
    }
    return original<T>(method, path, token, body);
  };
  let attempt = 0;
  const authority = new SessionAuthority(async () => (attempt++ ? b.gateway : a.gateway));
  await authority.connect(input);
  await authority.connect(input);
  let done = false;
  const disconnected = authority.disconnect().then(() => {
    done = true;
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(done, false);
  finish();
  await disconnected;
  assert.equal(a.closed(), 1);
  assert.equal(b.closed(), 1);
});
