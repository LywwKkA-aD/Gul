import test from 'node:test';
import assert from 'node:assert/strict';
import { openBrowserSession } from './browserSession.ts';

const code = 'a'.repeat(64);
const state = { epoch: 4, channelId: 1, serverOrigin: 'https://gul.example' };

test('browser companion exchanges one-use code and keeps every credential in authenticated same-origin requests', async () => {
  const calls = [];
  const session = await openBrowserSession(code, async (url, options) => {
    calls.push({ url, options });
    if (url.endsWith('/open')) return Response.json({ ...state, token: 'private-session' });
    if (url.endsWith('/state')) return Response.json(state);
    if (url.endsWith('/close')) return new Response(null, { status: 204 });
    return Response.json({ url: 'wss://gul.example', token: 'private-grant', identity: 'screen.1', ownerIdentity: 'voice.1', room: 'gul-channel-1', epoch: 4, channelId: 1 });
  });
  const grant = await session.grant();
  assert.equal(grant.identity, 'screen.1');
  await session.check();
  await session.close();
  assert.equal(calls[0].options.headers.Authorization, `Bearer ${code}`);
  assert.ok(calls.slice(1).every(({ options }) => options.headers.Authorization === 'Bearer private-session'));
  assert.ok(calls.every(({ url, options }) => url.startsWith('/api/screen/') && options.credentials === 'omit' && options.redirect === 'error'));
  assert.equal(calls.at(-1).options.keepalive, true);
  await assert.rejects(session.grant(), /Browser screen session ended/);
});

test('stale or lost companion state fails closed and does not expose response errors', async () => {
  for (const response of [Response.json({ ...state, epoch: 5 }), new Response(null, { status: 409 })]) {
    const session = await openBrowserSession(code, async (url) => url.endsWith('/open') ? Response.json({ ...state, token: 'private-session' }) : response);
    await assert.rejects(session.check(), /Browser screen session ended/);
  }
  await assert.rejects(openBrowserSession('secret', async () => { throw new Error('private-session'); }), /Invalid browser launch/);
});
