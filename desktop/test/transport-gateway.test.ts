import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { after, before } from 'node:test';
import tls from 'node:tls';
import { SignalResponse } from '@livekit/protocol';
import WebSocket, { WebSocketServer } from 'ws';
import { createRealityGateway, type Gateway } from '../src/transport/gateway.ts';
import * as publicTransport from '../src/transport/gateway.ts';
import { createGateway } from '../src/transport/gateway-core.ts';

const origin = 'gul://app';
const firstToken = 'fixture-initial-private-token';
const secondToken = 'fixture-refreshed-private-token';
const key = Buffer.alloc(32, 3).toString('base64url');
let folder: string, xray: string, ca: Buffer, server: tls.Server, port: number;
const accepted = new Set<net.Socket>();
const broker = http.createServer((req, res) => {
  if (
    req.url === '/api/gul/info' ||
    req.url === '/api/gul/members' ||
    req.url?.startsWith('/api/gul/channels/') ||
    req.url?.startsWith('/api/gul/invites/')
  ) {
    if (req.url === '/api/gul/channels/delete') {
      res.writeHead(409);
      res.end(JSON.stringify({ code: 'channel_busy', credential: 'private-unused' }));
      return;
    }
    let body = '';
    req.on('data', (chunk) => {
      body += chunk;
    });
    req.on('end', () =>
      res.end(
        JSON.stringify({
          method: req.method,
          path: req.url,
          body: body ? JSON.parse(body) : null,
          authorization: req.headers.authorization ?? '',
        }),
      ),
    );
    return;
  }
  if (req.url === '/healthz') {
    res.end(JSON.stringify({ status: 'ok' }));
    return;
  }
  if (req.url === '/api/gul/state') {
    res.end(JSON.stringify({ authorization: req.headers.authorization ?? '', host: req.headers.host }));
    return;
  }
  if (req.url === '/api/gul/login') {
    res.writeHead(401);
    res.end('private server error');
    return;
  }
  if (req.url === '/api/gul/channel') {
    res.writeHead(409);
    res.end('private server error');
    return;
  }
  if (req.url === '/api/gul/audio') {
    res.writeHead(302, { Location: 'https://private/?token=secret' });
    res.end();
    return;
  }
  if (req.url === '/api/gul/screen') {
    res.end('{malformed');
    return;
  }
  if (req.url === '/api/gul/logout') {
    res.end(Buffer.alloc(1024 * 1024 + 1, 65));
    return;
  }
  if (req.url?.startsWith('/rtc') && req.url.includes('/validate')) {
    res.end('valid');
    return;
  }
  res.end('{}');
});
const wsServer = new WebSocketServer({ noServer: true });
const remoteConnections = new Set<WebSocket>();
broker.on('upgrade', (req, socket, head) => {
  if (
    req.headers.authorization !== `Bearer ${firstToken}` &&
    req.headers.authorization !== `Bearer ${secondToken}`
  ) {
    socket.destroy();
    return;
  }
  assert.equal(new URL(req.url!, 'https://fixture').searchParams.has('access_token'), false);
  wsServer.handleUpgrade(req, socket, head, (ws) => {
    remoteConnections.add(ws);
    ws.on('close', () => remoteConnections.delete(ws));
    ws.send(
      new SignalResponse({
        message: {
          case: 'join',
          value: {
            iceServers: [{ urls: ['turns:127.0.0.1:443?transport=tcp'], username: 'u', credential: 'p' }],
          },
        },
      }).toBinary(),
    );
    ws.on('message', (data, binary) => {
      if (data.toString() === 'refresh')
        ws.send(new SignalResponse({ message: { case: 'refreshToken', value: secondToken } }).toBinary());
      else ws.send(data, { binary });
    });
  });
});

before(async () => {
  folder = mkdtempSync(join(tmpdir(), 'gul transport test '));
  execFileSync(
    'openssl',
    [
      'req',
      '-x509',
      '-newkey',
      'ec',
      '-pkeyopt',
      'ec_paramgen_curve:P-256',
      '-nodes',
      '-days',
      '1',
      '-keyout',
      join(folder, 'key.pem'),
      '-out',
      join(folder, 'cert.pem'),
      '-subj',
      '/CN=fixture',
      '-addext',
      'subjectAltName=IP:127.0.0.1',
    ],
    { stdio: 'ignore' },
  );
  ca = readFileSync(join(folder, 'cert.pem'));
  server = tls.createServer({ cert: ca, key: readFileSync(join(folder, 'key.pem')) }, (socket) => {
    accepted.add(socket);
    socket.on('error', () => undefined);
    socket.on('close', () => accepted.delete(socket));
    socket.once('data', (first) => {
      if (first[0] === 0) {
        socket.write(first);
        socket.on('data', (data) => socket.write(data));
      } else {
        socket.pause();
        socket.unshift(first);
        broker.emit('connection', socket);
        socket.resume();
      }
    });
  });
  server.on('connection', (socket) => {
    accepted.add(socket);
    socket.on('close', () => accepted.delete(socket));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  port = (server.address() as net.AddressInfo).port;
  xray = join(folder, 'fake-xray.cjs');
  const source = String.raw`
const net = require('node:net');
let input = ''; process.stdin.on('data', b => input += b); process.stdin.on('end', () => {
  const config = JSON.parse(input), inbound = config.inbounds[0], credentials = inbound.settings.accounts[0];
  const outer = config.outbounds[0].settings.vnext[0];
  const connections = new Set();
  const server = net.createServer(socket => {
    connections.add(socket); socket.on('error', () => {}); socket.on('close', () => connections.delete(socket));
    let state = 0, buffer = Buffer.alloc(0);
    function consume(data) {
      buffer = Buffer.concat([buffer, data]);
      if (state === 0 && buffer.length >= 3) {
        if (!buffer.subarray(0, 3).equals(Buffer.from([5, 1, 2]))) return socket.destroy();
        buffer = buffer.subarray(3); state = 1; socket.write(Buffer.from([5, 2]));
      }
      if (state === 1 && buffer.length >= 2) {
        const n = buffer[1]; if (buffer.length < n + 3) return;
        const m = buffer[n + 2]; if (buffer.length < n + m + 3) return;
        const ok = buffer[0] === 1 && buffer.subarray(2, n + 2).toString() === credentials.user && buffer.subarray(n + 3, n + m + 3).toString() === credentials.pass;
        if (!ok) { socket.end(Buffer.from([1, 1])); return; }
        buffer = buffer.subarray(n + m + 3); state = 2; socket.write(Buffer.from([1, 0]));
      }
      if (state === 2 && buffer.length >= 10) {
        if (!buffer.subarray(0, 10).equals(Buffer.from([5, 1, 0, 1, 127, 0, 0, 1, 1, 187]))) return socket.destroy();
        const rest = buffer.subarray(10); state = 3; socket.removeListener('data', consume);
        const upstream = net.connect(outer.port, '127.0.0.1');
        upstream.on('error', () => socket.destroy()); socket.on('close', () => upstream.destroy());
        upstream.once('connect', () => { socket.write(Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 0, 0])); if (rest.length) upstream.write(rest); socket.pipe(upstream).pipe(socket); });
      }
    }
    socket.on('data', consume);
  });
  server.listen(inbound.port, inbound.listen);
  process.on('SIGTERM', () => { for (const socket of connections) socket.destroy(); server.close(() => process.exit(0)); });
});`;
  writeFileSync(xray, source, { mode: 0o600 });
});
after(async () => {
  for (const ws of remoteConnections) ws.terminate();
  for (const socket of accepted) socket.destroy();
  await new Promise<void>((resolve) => server.close(() => resolve()));
  wsServer.close();
  broker.close();
  rmSync(folder, { recursive: true, force: true });
});
function address() {
  return `livekit+vless://127.0.0.1:${port}?security=reality&flow=none&type=tcp&sni=cover.example&pbk=${key}&sid=01ab`;
}
function openGateway(options = {}) {
  return createGateway(
    {
      address: address(),
      password: 'fixture-password',
      origin,
      xrayPath: xray,
      ca,
      ...options,
    },
    (file, args) => {
      assert.deepEqual(args, ['run', '-config', 'stdin:']);
      return spawn(process.execPath, [file, ...args], {
        stdio: ['pipe', 'ignore', 'ignore'],
        windowsHide: true,
      });
    },
  );
}
function signal(gateway: Gateway, token = firstToken) {
  const base = gateway.signalURL(1, token);
  return `${base}/rtc?access_token=${token}`;
}
function connectWS(url: string, suppliedOrigin = origin) {
  return new WebSocket(url, { origin: suppliedOrigin });
}
async function firstJoin(ws: WebSocket) {
  const [bytes] = await once(ws, 'message');
  return SignalResponse.fromBinary(Buffer.from(bytes));
}

test('broker stays pinned and TLS verified; errors never expose server bodies or credentials', async () => {
  const gateway = await openGateway();
  try {
    assert.equal(gateway.brokerOrigin, 'https://127.0.0.1');
    assert.deepEqual(await gateway.request('GET', '/healthz'), { status: 'ok' });
    assert.deepEqual(await gateway.request('GET', '/api/gul/state', 'private-broker-token'), {
      authorization: 'Bearer private-broker-token',
      host: '127.0.0.1',
    });
    for (const [path, code] of [
      ['login', 'authentication'],
      ['channel', 'stale'],
      ['audio', 'server'],
    ]) {
      await assert.rejects(
        gateway.request('POST', `/api/gul/${path}`),
        (error: unknown) =>
          error instanceof Error &&
          'code' in error &&
          error.code === code &&
          !/private|secret/.test(String(error)),
      );
    }
    for (const path of ['https://outside/', '/api/gul/state?x=1', '/api/gul/../state', '/admin']) {
      await assert.rejects(gateway.request('GET', path));
    }
    await assert.rejects(gateway.request('GET', '/api/gul/state', 'secret\r\nInjected: true'));
    await assert.rejects(gateway.request('POST', '/api/gul/screen'), /Сервер не ответил корректно/);
    await assert.rejects(gateway.request('POST', '/api/gul/logout'), /Сервер не ответил корректно/);
    await assert.rejects(gateway.request('POST', '/api/gul/channel', undefined, { data: 'x'.repeat(65537) }));
    const cyclic: { self?: unknown } = {};
    cyclic.self = cyclic;
    await assert.rejects(gateway.request('POST', '/api/gul/channel', undefined, cyclic));
  } finally {
    await gateway.close();
  }
  const untrusted = await openGateway({ ca: undefined });
  try {
    await assert.rejects(untrusted.request('GET', '/healthz'), /Не удалось подключиться через REALITY/);
  } finally {
    await untrusted.close();
  }
  const wrongAuthority = await openGateway({ address: address().replace('127.0.0.1:', '127.0.0.2:') });
  try {
    await assert.rejects(wrongAuthority.request('GET', '/healthz'), /Не удалось подключиться через REALITY/);
  } finally {
    await wrongAuthority.close();
  }
});

test('signaling authorizes exact local authority, origin, capability and JWT; refresh and stale epochs close safely', async () => {
  const gateway = await openGateway();
  gateway.beginEpoch(1);
  try {
    const url = signal(gateway);
    const rejected = [url.replace(firstToken, 'unknown-private-token'), url.replace('/rtc?', '/other?')];
    for (const bad of rejected) {
      const ws = connectWS(bad);
      await assert.rejects(once(ws, 'open'));
      ws.terminate();
    }
    const foreign = connectWS(url, 'https://evil.example');
    await assert.rejects(once(foreign, 'open'));
    foreign.terminate();
    for (const bad of [
      new WebSocket(url, { origin, headers: { Host: 'foreign.example' } }),
      new WebSocket(url + `&access_token=${firstToken}`, { origin }),
      new WebSocket(url, { origin, headers: { Authorization: 'Bearer wrong' } }),
    ]) {
      await assert.rejects(once(bad, 'open'));
      bad.terminate();
    }
    const validation = url.replace('ws:', 'http:').replace('/rtc?', '/rtc/validate?');
    const validated = await new Promise<{ status: number; body: string }>((resolve, reject) => {
      http
        .get(validation, { headers: { Origin: origin } }, (response) => {
          let body = '';
          response.on('data', (data) => {
            body += data;
          });
          response.on('end', () => resolve({ status: response.statusCode!, body }));
        })
        .on('error', reject);
    });
    assert.deepEqual(validated, { status: 200, body: 'valid' });
    const valid = connectWS(url);
    const joinMessage = await firstJoin(valid);
    assert.equal(joinMessage.message.case, 'join');
    const refresh = once(valid, 'message');
    valid.send('refresh');
    const [data] = await refresh;
    assert.equal(SignalResponse.fromBinary(Buffer.from(data)).message.case, 'refreshToken');
    const next = connectWS(signal(gateway, secondToken));
    await firstJoin(next);
    const closed = Promise.all([once(valid, 'close'), once(next, 'close')]);
    gateway.beginEpoch(2);
    await closed;
    assert.throws(() => gateway.signalURL(1, firstToken), /Канал изменился/);
    const stale = connectWS(url);
    await assert.rejects(once(stale, 'open'));
    stale.terminate();
    assert.deepEqual(await gateway.request('GET', '/healthz'), { status: 'ok' });
    gateway.beginEpoch(2);
  } finally {
    await gateway.close();
  }
  await assert.rejects(gateway.request('GET', '/healthz'), /Подключение закрыто/);
});

test('TURN validates the first fragmented STUN header before fixed TLS forwarding, and closes on epoch change', async () => {
  const gateway = await openGateway();
  gateway.beginEpoch(1);
  const ws = connectWS(signal(gateway));
  try {
    const message = await firstJoin(ws);
    assert.equal(message.message.case, 'join');
    if (message.message.case !== 'join') throw new Error('missing join');
    const turnPort = Number(/127\.0\.0\.1:(\d+)/.exec(message.message.value.iceServers[0].urls[0])![1]);
    const connect = async () => {
      const socket = net.connect(turnPort, '127.0.0.1');
      socket.on('error', () => undefined);
      await once(socket, 'connect');
      return socket;
    };
    let epoch = 1;
    for (const kind of [0x0001, 0x0003]) {
      const socket = await connect();
      const packet = Buffer.alloc(20);
      packet.writeUInt16BE(kind);
      packet.writeUInt32BE(0x2112a442, 4);
      const echoed = once(socket, 'data');
      socket.write(packet.subarray(0, 7));
      socket.write(packet.subarray(7));
      assert.deepEqual((await echoed)[0], packet);
      const closed = once(socket, 'close');
      gateway.beginEpoch(++epoch);
      await closed;
    }
    for (const packet of [
      Buffer.alloc(20),
      Buffer.from('GET / HTTP/1.1\r\n\r\n'),
      (() => {
        const p = Buffer.alloc(20);
        p.writeUInt16BE(3);
        p.writeUInt16BE(1, 2);
        p.writeUInt32BE(0x2112a442, 4);
        return p;
      })(),
    ]) {
      const socket = await connect();
      const closed = once(socket, 'close');
      socket.write(packet);
      await closed;
    }
  } finally {
    ws.terminate();
    await gateway.close();
  }
});

test('missing Xray and invalid runtime origin fail with safe errors', async () => {
  await assert.rejects(
    openGateway({ xrayPath: join(folder, 'missing-secret-xray') }),
    /Не удалось подключиться через REALITY/,
  );
  await assert.rejects(openGateway({ origin: 'https://untrusted' }));
});

test('public gateway factory exposes no child launcher injection', async () => {
  assert.deepEqual(Object.keys(publicTransport), ['createRealityGateway']);
  let injected = false;
  const options = {
    address: address(),
    password: 'fixture-password',
    origin,
    xrayPath: join(folder, 'missing-native-binary'),
    launchXray: () => {
      injected = true;
      throw new Error('private injected failure');
    },
  };
  await assert.rejects(createRealityGateway(options), /Не удалось подключиться через REALITY/);
  assert.equal(injected, false);
});

test(
  'optional real REALITY fixture validates read-only health without direct fallback',
  { skip: !process.env.GUL_DESKTOP_REALITY_ADDRESS_FILE },
  async () => {
    const options = {
      address: readFileSync(process.env.GUL_DESKTOP_REALITY_ADDRESS_FILE!, 'utf8').trim(),
      password: readFileSync(process.env.GUL_DESKTOP_REALITY_PASSWORD_FILE!, 'utf8').replace(/\r?\n$/, ''),
      origin,
      xrayPath: process.env.GUL_DESKTOP_XRAY_PATH!,
      ca: process.env.GUL_DESKTOP_REALITY_CA_FILE
        ? readFileSync(process.env.GUL_DESKTOP_REALITY_CA_FILE)
        : undefined,
    };
    const gateway = await createRealityGateway(options);
    try {
      const health = await gateway.request<{ status: string }>('GET', '/healthz');
      assert.equal(health.status, 'ok');
    } finally {
      await gateway.close();
    }
    for (const invalid of [
      { ...options, password: options.password + '-invalid-fixture-password' },
      {
        ...options,
        address: options.address.replace(/pbk=[^&]+/, `pbk=${Buffer.alloc(32, 7).toString('base64url')}`),
      },
    ]) {
      const rejected = await createRealityGateway(invalid);
      try {
        await assert.rejects(rejected.request('GET', '/healthz'), /Не удалось подключиться через REALITY/);
      } finally {
        await rejected.close();
      }
    }
  },
);

test('management endpoints use fixed methods and pinned broker transport; error DTOs are sanitized', async () => {
  const gateway = await openGateway();
  try {
    for (const path of ['/api/gul/info', '/api/gul/members']) {
      const result = await gateway.request<{ method: string; path: string }>(
        'GET',
        path,
        'fixed-broker-token',
      );
      assert.equal(result.method, 'GET');
      assert.equal(result.path, path);
      await assert.rejects(gateway.request('POST', path));
      await assert.rejects(gateway.request('GET', path + '?path=outside'));
    }
    for (const route of [
      'channels/create',
      'channels/update',
      'channels/permissions',
      'invites/create',
      'invites/redeem',
    ]) {
      const path = '/api/gul/' + route,
        result = await gateway.request<{ body: unknown; authorization: string }>(
          'POST',
          path,
          'fixed-broker-token',
          { name: 'Bounded' },
        );
      assert.deepEqual(result.body, { name: 'Bounded' });
      assert.equal(result.authorization, 'Bearer fixed-broker-token');
      await assert.rejects(gateway.request('GET', path));
    }
    await assert.rejects(
      gateway.request('POST', '/api/gul/channels/delete', 'fixed-broker-token', {}),
      (error: Error) => error.message === 'Канал занят; удаление недоступно',
    );
  } finally {
    await gateway.close();
  }
});
