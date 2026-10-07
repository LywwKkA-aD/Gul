import assert from 'node:assert/strict';
import test from 'node:test';
import { deriveUserID, parseRealityProfile, xrayConfig } from '../src/transport/profile.ts';

const key = Buffer.alloc(32, 3).toString('base64url');
const address = `livekit+vless://192.0.2.8:8443?security=reality&flow=none&type=tcp&sni=cover.example&pbk=${key}&sid=01ab`;

test('public profile keeps outer port separate from fixed HTTPS authority', () => {
  const p = parseRealityProfile(address);
  assert.equal(p.host, '192.0.2.8');
  assert.equal(p.port, 8443);
  assert.equal(p.origin, 'https://192.0.2.8');
  assert.equal(p.sni, 'cover.example');
  assert.equal(deriveUserID('gul-test-password'), '0b70bd5c-254d-8d93-8a14-98359d1ad0fb');
  assert.notEqual(deriveUserID(' password'), deriveUserID('password'));
  assert.equal(
    parseRealityProfile(address.replace('192.0.2.8', '[2001:db8::8]')).origin,
    'https://[2001:db8::8]',
  );
});

test('unsafe, credential-bearing and ambiguous profiles expose only safe errors', () => {
  const secret = 'never-log-this-secret';
  for (const raw of [
    address.replace('livekit+vless:', 'vless:'),
    address + '&sid=01ab',
    address + `&password=${secret}`,
    address.replace('//192.0.2.8', `//${secret}@192.0.2.8`),
    address + '#' + secret,
    address.replace('?security', '/path?security'),
    address.replace('flow=none', 'flow=vision'),
    address.replace('sid=01ab', 'sid=%30%31ab'),
    address.replace('sni=cover.example', 'sni=192.0.2.1'),
    address.replace(':8443?', ':0?'),
    address.replace('192.0.2.8', '999.0.0.1'),
    address.replace(key, key + '='),
    address.replace('sid=01ab', 'sid=ABCDEF'),
    address.replace('192.0.2.8', '0.0.0.0'),
    address.replace('192.0.2.8', '224.0.0.1'),
  ]) {
    assert.throws(
      () => parseRealityProfile(raw),
      (e: unknown) => e instanceof Error && e.message === 'Некорректный профиль REALITY',
    );
  }
});

test('Xray config has authenticated local SOCKS, no UDP/mux/direct route, fixed remote target', () => {
  const config = xrayConfig(
    parseRealityProfile(address),
    'gul-test-password',
    12345,
    'local-user',
    'local-pass',
  );
  const inbound = config.inbounds[0];
  assert.equal(inbound.listen, '127.0.0.1');
  assert.equal(inbound.settings.auth, 'password');
  assert.deepEqual(inbound.settings.accounts, [{ user: 'local-user', pass: 'local-pass' }]);
  assert.equal(inbound.settings.udp, false);
  assert.equal(config.outbounds[0].mux!.enabled, false);
  assert.equal(config.outbounds[0].settings!.vnext[0].users[0].id, deriveUserID('gul-test-password'));
  assert.equal(config.outbounds[0].streamSettings!.realitySettings.password, key);
  assert.deepEqual(config.routing.rules[0], {
    type: 'field',
    ip: ['127.0.0.1'],
    port: '443',
    network: 'tcp',
    outboundTag: 'reality',
  });
  assert.equal(config.routing.rules[1].outboundTag, 'blocked');
  assert.equal(config.outbounds[1].protocol, 'blackhole');
  assert.equal(JSON.stringify(config).includes('gul-test-password'), false);
});
