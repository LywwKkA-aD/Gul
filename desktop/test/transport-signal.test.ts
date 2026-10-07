import assert from 'node:assert/strict';
import test from 'node:test';
import { ClientConfiguration, ClientConfigSetting, SignalResponse } from '@livekit/protocol';
import { rewriteSignal, trustedTURN } from '../src/transport/signal.ts';

const remote = '192.0.2.8';
const local = '127.0.0.1:12345';
function response(kind: 'join' | 'reconnect', urls = [`turns:${remote}:443?transport=tcp`]) {
  return new SignalResponse({
    message: {
      case: kind,
      value: {
        iceServers: [{ urls, username: 'turn-user', credential: 'turn-pass' }],
        clientConfiguration: { resumeConnection: ClientConfigSetting.DISABLED },
      },
    },
  });
}

test('Join and reconnect replace all ICE routes with authenticated loopback TURN/TCP', () => {
  for (const kind of ['join', 'reconnect'] as const)
    for (const binary of [true, false]) {
      const input = response(kind, [`stun:outside.example:3478`, `turns:${remote}:443`]);
      const bytes = binary ? input.toBinary() : Buffer.from(input.toJsonString());
      const output = rewriteSignal(bytes, binary, remote, local, () => undefined);
      const parsed = binary
        ? SignalResponse.fromBinary(output)
        : SignalResponse.fromJsonString(Buffer.from(output).toString());
      assert.equal(parsed.message.case, kind);
      if (parsed.message.case === 'join' || parsed.message.case === 'reconnect') {
        assert.deepEqual(parsed.message.value.iceServers[0].urls, [`turn:${local}?transport=tcp`]);
        assert.equal(parsed.message.value.iceServers.length, 1);
        assert.equal(parsed.message.value.iceServers[0].username, 'turn-user');
        assert.equal(parsed.message.value.iceServers[0].credential, 'turn-pass');
        assert.equal(parsed.message.value.clientConfiguration?.forceRelay, ClientConfigSetting.ENABLED);
        assert.equal(
          parsed.message.value.clientConfiguration?.resumeConnection,
          ClientConfigSetting.DISABLED,
        );
      }
    }
});

test('TURN trust accepts only the profile authority and TCP TLS port 443', () => {
  assert.equal(trustedTURN(`turns:${remote}:443`, remote), true);
  assert.equal(trustedTURN('turns:[2001:db8::8]:443?transport=tcp', '2001:db8::8'), true);
  assert.equal(trustedTURN('turns:EXAMPLE.ORG:443', 'example.org'), true);
  for (const url of [
    `turn:${remote}:443`,
    `turns:${remote}:8443`,
    `turns:evil.example:443`,
    `turns://${remote}:443`,
    `turns:user@${remote}:443`,
    `turns:${remote}:443#secret`,
    `turns:${remote}:443/path`,
    `turns:${remote}:443?transport=udp`,
    `turns:${remote}:443?transport=tcp&transport=tcp`,
    `turns:${remote}:443?x=1`,
    `turns:${remote}:443?%74ransport=tcp`,
    `turns:${remote}:443?transport=tcp&`,
  ]) {
    assert.equal(trustedTURN(url, remote), false, url);
    assert.throws(() =>
      rewriteSignal(response('join', [url]).toBinary(), true, remote, local, () => undefined),
    );
  }
});

test('ICE rewriting preserves future protobuf client configuration fields', () => {
  const input = response('join');
  if (input.message.case !== 'join') throw new Error('join expected');
  const unknown = Buffer.from([0x98, 0x06, 0x07]);
  input.message.value.clientConfiguration = ClientConfiguration.fromBinary(unknown);
  const rewritten = SignalResponse.fromBinary(
    rewriteSignal(input.toBinary(), true, remote, local, () => undefined),
  );
  if (rewritten.message.case !== 'join') throw new Error('join expected');
  const config = rewritten.message.value.clientConfiguration!;
  assert.equal(config.forceRelay, ClientConfigSetting.ENABLED);
  assert.equal(Buffer.from(config.toBinary()).includes(unknown), true);
});

test('redirects and malformed messages fail closed; refreshed tokens register without changing bytes', () => {
  const refresh = new SignalResponse({
    message: { case: 'refreshToken', value: 'refreshed-private-token' },
  }).toBinary();
  const registered: string[] = [];
  assert.deepEqual(
    rewriteSignal(refresh, true, remote, local, (token) => {
      registered.push(token);
    }),
    refresh,
  );
  assert.deepEqual(registered, ['refreshed-private-token']);
  assert.throws(
    () =>
      rewriteSignal(refresh, true, remote, local, () => {
        throw new Error('private');
      }),
    /Сервер не ответил корректно/,
  );
  const moved = new SignalResponse({ message: { case: 'roomMoved', value: {} } });
  const alternate = response('join');
  if (alternate.message.case === 'join')
    alternate.message.value.alternativeUrl = 'wss://private.example/?token=private';
  for (const data of [
    moved.toBinary(),
    alternate.toBinary(),
    Buffer.from([255]),
    Buffer.alloc(1024 * 1024 + 1),
  ]) {
    assert.throws(
      () => rewriteSignal(data, true, remote, local, () => undefined),
      /Сервер не ответил корректно/,
    );
  }
  assert.throws(() => rewriteSignal(Buffer.from('{invalid'), false, remote, local, () => undefined));
  const ping = new SignalResponse({ message: { case: 'pong', value: 42n } }).toBinary();
  assert.deepEqual(
    rewriteSignal(ping, true, remote, local, () => undefined),
    ping,
  );
  const leave = new SignalResponse({
    message: { case: 'leave', value: { regions: { regions: [{ url: 'wss://unsafe' }] } } },
  });
  const out = SignalResponse.fromBinary(
    rewriteSignal(leave.toBinary(), true, remote, local, () => undefined),
  );
  if (out.message.case === 'leave') assert.equal(out.message.value.regions, undefined);
});
