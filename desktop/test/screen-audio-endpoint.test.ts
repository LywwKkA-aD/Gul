import assert from 'node:assert/strict';
import test from 'node:test';
import { localPulseEndpoint, resolvePulseEndpoint } from '../src/main/screen-audio-endpoint.ts';

test('only one explicit local Unix socket can be an audio endpoint', () => {
  for (const value of ['unix:/run/user/1000/pulse/native', '/run/user/1000/pulse/native'])
    assert.equal(localPulseEndpoint(value), 'unix:/run/user/1000/pulse/native');
  for (const value of [
    'unix:/missing tcp:host:4713',
    'tcp:localhost:4713',
    'unix:/socket\ntcp:host',
    'unix:/socket:tcp:host',
    'unix:/socket\0',
    'relative/socket',
    '',
    null,
  ])
    assert.equal(localPulseEndpoint(value), null);
});
test('the helper pins a detected current-UID socket and never uses client.conf or an alternate server list', async () => {
  const probes: string[] = [];
  const probe = async (path: string, uid: number) => {
    probes.push(path);
    assert.equal(uid, 1000);
    return path === '/run/user/1000/pulse/native';
  };
  assert.equal(await resolvePulseEndpoint({}, 1000, probe), 'unix:/run/user/1000/pulse/native');
  assert.deepEqual(probes, ['/run/user/1000/pulse/native']);
  probes.length = 0;
  assert.equal(
    await resolvePulseEndpoint({ PULSE_SERVER: 'unix:/missing tcp:host:4713' }, 1000, probe),
    null,
  );
  assert.deepEqual(probes, []);
  assert.equal(await resolvePulseEndpoint({ PULSE_SERVER: 'unix:/missing' }, 1000, probe), null);
  assert.deepEqual(probes, ['/missing']);
  assert.equal(
    await resolvePulseEndpoint(
      { PULSE_RUNTIME_PATH: '/other/pulse', XDG_RUNTIME_DIR: '/run/user/1000' },
      1000,
      probe,
    ),
    'unix:/run/user/1000/pulse/native',
  );
});
