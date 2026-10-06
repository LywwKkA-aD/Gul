import test from 'node:test';
import assert from 'node:assert/strict';
import { screenSession, sessionGrantProvider } from './session.ts';

const connected = { state: 'connected', server: 'https://gul.example', epoch: 7, selfChannel: 0 };
const validGrant = {
  url: 'wss://gul.example', token: 'private-token', identity: 'screen-alice',
  room: 'channel-0', ownerIdentity: 'alice', channelId: 0, epoch: 7,
};

test('screen session exists only for a confirmed connected epoch and channel, including channel zero', () => {
  assert.deepEqual(screenSession(connected), { epoch: 7, channelId: 0, serverOrigin: 'https://gul.example', key: 'https://gul.example|7:0' });
  for (const patch of [
    { state: 'connecting' }, { state: 'reconnecting' }, { state: 'disconnected' },
    { epoch: undefined }, { epoch: 0 }, { epoch: -1 }, { epoch: NaN },
    { epoch: Number.MAX_SAFE_INTEGER + 1 },
    { selfChannel: undefined }, { selfChannel: -1 }, { selfChannel: 1.5 }, { selfChannel: 2 ** 32 },
  ]) assert.equal(screenSession({ ...connected, ...patch }), null);
});

test('channel changes and fresh logins produce a new panel lifetime even on the same channel', () => {
  assert.notEqual(screenSession(connected).key, screenSession({ ...connected, selfChannel: 2 }).key);
  assert.notEqual(screenSession(connected).key, screenSession({ ...connected, epoch: 8 }).key);
});

test('screen grant is requested for the captured Go session and uses server-owned identities', async () => {
  const calls = [];
  const provider = sessionGrantProvider(screenSession(connected), async (...args) => {
    calls.push(args);
    return validGrant;
  });
  const grant = await provider();
  assert.deepEqual(calls, [[7, 0]]);
  assert.equal(grant.identity, 'screen-alice');
  assert.equal(grant.ownerIdentity, 'alice');
});

test('stale, malformed or credential-bearing grants are rejected without exposing token content', async () => {
  for (const patch of [
    { epoch: 8 }, { channelId: 1 }, { token: '' }, { identity: '' }, { room: '' },
    { ownerIdentity: '' }, { identity: 'alice' },
    { url: 'https://gul.example' }, { url: 'ws://user:private-token@gul.example' },
    { url: 'wss://gul.example/?token=private-token' }, { url: 'wss://gul.example/#private-token' },
  ]) {
    const provider = sessionGrantProvider(screenSession(connected), async () => ({ ...validGrant, ...patch }));
    await assert.rejects(provider(), (error) => {
      assert.equal(error.message, 'Screen session is no longer available');
      assert.equal(error.code, 'SCREEN_GRANT_INVALID');
      assert.ok(!error.message.includes('private-token'));
      return true;
    });
  }
});

test('broker errors cannot leak into screen session errors', async () => {
  const provider = sessionGrantProvider(screenSession(connected), async () => { throw new Error('private-token'); });
  await assert.rejects(provider(), { message: 'Screen session is no longer available', code: 'SCREEN_GRANT_REQUEST' });
});

test('remote screen grants must stay on authenticated broker authority and use WSS', async () => {
  for (const url of ['ws://gul.example', 'wss://other.example', 'wss://gul.example:444', 'wss://gul.example/other', 'ws://127.0.0.1:7880']) {
    const provider = sessionGrantProvider(screenSession(connected), async () => ({ ...validGrant, url }));
    await assert.rejects(provider(), { message: 'Screen session is no longer available' });
  }
  const provider = sessionGrantProvider(screenSession({ ...connected, server: 'https://GUL.example:443/' }),
    async () => ({ ...validGrant, url: 'wss://gul.example:443/' }));
  assert.equal((await provider()).identity, validGrant.identity);
  const explicitPort = sessionGrantProvider(screenSession({ ...connected, server: 'https://gul.example:8443' }),
    async () => ({ ...validGrant, url: 'wss://gul.example:8443' }));
  assert.equal((await explicitPort()).identity, validGrant.identity);
});

test('local screens use only the pinned local broker and SFU ports', async () => {
  const local = screenSession({ ...connected, server: 'http://127.0.0.1:8787' });
  assert.ok(local);
  const provider = sessionGrantProvider(local, async () => ({ ...validGrant, url: 'ws://127.0.0.1:7880' }));
  await provider();
  for (const url of ['ws://127.0.0.1:7881', 'ws://localhost:7880', 'wss://gul.example', 'ws://127.0.0.1:7880/rtc']) {
    await assert.rejects(sessionGrantProvider(local, async () => ({ ...validGrant, url }))());
  }
});

test('invalid broker origins cannot create a screen session and a server switch changes its key', () => {
  for (const server of ['http://gul.example', 'https://user:private-token@gul.example', 'https://gul.example/path', 'https://gul.example/?token=private-token', 'invalid']) {
    assert.equal(screenSession({ ...connected, server }), null);
  }
  assert.notEqual(screenSession(connected).key, screenSession({ ...connected, server: 'https://other.example' }).key);
});

test('REALITY profiles permit only explicitly tagged local capability gateways with forced relay', async () => {
  const profile = 'livekit+vless://gul.example:8443?security=reality&flow=none&type=tcp&sni=cover.example&pbk=' + 'A'.repeat(43) + '&sid=1234';
  const session = screenSession({ ...connected, server: profile });
  assert.equal(session.serverOrigin, 'https://gul.example');
  assert.equal(session.transport, 'reality');
  const gateway = { ...validGrant, url: `ws://127.0.0.1:41321/${'a'.repeat(64)}`, transport: 'reality', relayOnly: true };
  assert.equal((await sessionGrantProvider(session, async () => gateway)()).url, gateway.url);
  for (const patch of [{ transport: '' }, { relayOnly: false }, { url: gateway.url.replace('127.0.0.1', 'localhost') }, { url: 'ws://127.0.0.1:41321/short' }, { url: `${gateway.url}?token=private` }]) {
    await assert.rejects(sessionGrantProvider(session, async () => ({ ...gateway, ...patch }))());
  }
  await assert.rejects(sessionGrantProvider(screenSession(connected), async () => gateway)());
});
