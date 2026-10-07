import assert from 'node:assert/strict';
import { once } from 'node:events';
import net from 'node:net';
import { PassThrough } from 'node:stream';
import test from 'node:test';
import { authenticateSOCKS, connectFixedTarget, readExactly } from '../src/transport/socks.ts';
import { EpochTokens } from '../src/transport/tokens.ts';
import { validInitialSTUN } from '../src/transport/turn.ts';

test('epochs reject stale callbacks and keep the pinned voice token through bounded screen refreshes', () => {
  const tokens = new EpochTokens();
  assert.throws(() => tokens.register(0, 'token'));
  for (const epoch of [0, -1, 0.5, Number.NaN]) assert.throws(() => tokens.begin(epoch));
  tokens.begin(1);
  tokens.register(1, 'voice');
  tokens.register(1, 'screen');
  const capability = tokens.capability;
  tokens.refresh(1, 'screen', 'screen-next');
  assert.equal(tokens.accepts('screen'), false);
  assert.equal(tokens.accepts('screen-next'), true);
  assert.equal(tokens.accepts('voice'), true);
  for (let index = 0; index < 64; index++) tokens.register(1, `screen-${index}`);
  assert.equal(tokens.accepts('voice'), true);
  assert.equal(tokens.accepts('screen-0'), false);
  assert.equal(tokens.accepts('screen-63'), true);
  assert.equal(tokens.begin(1), false);
  assert.equal(tokens.capability, capability);
  tokens.begin(2);
  assert.notEqual(tokens.capability, capability);
  assert.equal(tokens.accepts('voice'), false);
  assert.throws(() => tokens.begin(1));
  assert.throws(() => tokens.refresh(1, 'voice', 'late-token'));
  for (const invalid of ['', 'token\r\nSecret', 'x'.repeat(16385)])
    assert.throws(() => tokens.register(2, invalid));
  tokens.clear();
  assert.equal(tokens.capability, '');
  assert.equal(tokens.accepts('screen-63'), false);
});

test('bounded header reader preserves following bytes and rejects truncation/deadlines', async () => {
  const socket = new PassThrough() as unknown as net.Socket;
  const read = readExactly(socket, 4, 100);
  socket.write(Buffer.from([1, 2]));
  socket.write(Buffer.from([3, 4, 5]));
  assert.deepEqual(await read, Buffer.from([1, 2, 3, 4]));
  assert.deepEqual(socket.read(1), Buffer.from([5]));
  socket.destroy();
  const incomplete = new PassThrough() as unknown as net.Socket;
  const truncated = readExactly(incomplete, 20, 100);
  incomplete.end(Buffer.alloc(7));
  await assert.rejects(truncated, /Не удалось подключиться через REALITY/);
  incomplete.destroy();
  const stalled = new PassThrough() as unknown as net.Socket;
  await assert.rejects(readExactly(stalled, 20, 5), /Не удалось подключиться через REALITY/);
  stalled.destroy();
  const ended = new PassThrough() as unknown as net.Socket;
  ended.destroy();
  await assert.rejects(readExactly(ended, 1, 5));
});

async function withSocket(handler: (socket: net.Socket) => void, use: (socket: net.Socket) => Promise<void>) {
  const sockets = new Set<net.Socket>();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on('error', () => undefined);
    socket.on('close', () => sockets.delete(socket));
    handler(socket);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const socket = net.connect((server.address() as net.AddressInfo).port, '127.0.0.1');
  socket.on('error', () => undefined);
  await once(socket, 'connect');
  try {
    await use(socket);
  } finally {
    socket.destroy();
    for (const peer of sockets) peer.destroy();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
}

test('SOCKS negotiation requires password authentication and rejects failed method/auth responses', async () => {
  for (const bad of [Buffer.from([5, 0]), Buffer.from([4, 2])]) {
    await withSocket(
      (socket) => socket.once('data', () => socket.end(bad)),
      async (socket) => {
        await assert.rejects(authenticateSOCKS(socket, 'user', 'password'));
      },
    );
  }
  for (const password of ['password', '', 'x'.repeat(256)]) {
    await withSocket(
      (socket) =>
        socket.once('data', () => {
          socket.write(Buffer.from([5, 2]));
          socket.once('data', () => socket.end(Buffer.from([1, 1])));
        }),
      async (socket) => {
        await assert.rejects(authenticateSOCKS(socket, 'user', password));
      },
    );
  }
});

test('SOCKS CONNECT always requests IPv4 localhost:443 and consumes bounded reply addresses', async () => {
  const replies = [
    Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 1, 187]),
    Buffer.from([5, 0, 0, 3, 3, 97, 98, 99, 1, 187]),
    Buffer.concat([Buffer.from([5, 0, 0, 4]), Buffer.alloc(16), Buffer.from([1, 187])]),
  ];
  for (const reply of replies)
    await withSocket(
      (socket) =>
        socket.once('data', (request) => {
          assert.deepEqual(request, Buffer.from([5, 1, 0, 1, 127, 0, 0, 1, 1, 187]));
          socket.write(reply.subarray(0, 2));
          socket.write(Buffer.concat([reply.subarray(2), Buffer.from([99])]));
        }),
      async (socket) => {
        await connectFixedTarget(socket);
        assert.deepEqual(await readExactly(socket, 1), Buffer.from([99]));
      },
    );
  for (const reply of [
    Buffer.from([5, 1, 0, 1]),
    Buffer.from([4, 0, 0, 1]),
    Buffer.from([5, 0, 1, 1]),
    Buffer.from([5, 0, 0, 9]),
    Buffer.from([5, 0, 0, 3, 0]),
  ]) {
    await withSocket(
      (socket) => socket.once('data', () => socket.end(reply)),
      async (socket) => {
        await assert.rejects(connectFixedTarget(socket));
      },
    );
  }
});

test('TURN recognizes only Binding/Allocate STUN requests and aligned RFC message lengths', () => {
  assert.equal(validInitialSTUN(Buffer.alloc(19)), false);
  for (const kind of [1, 3, 4, 0x0101, 0x4000]) {
    const header = Buffer.alloc(20);
    header.writeUInt16BE(kind);
    header.writeUInt32BE(0x2112a442, 4);
    assert.equal(validInitialSTUN(header), kind === 1 || kind === 3);
    header.writeUInt16BE(65532, 2);
    assert.equal(validInitialSTUN(header), kind === 1 || kind === 3);
    header.writeUInt16BE(65535, 2);
    assert.equal(validInitialSTUN(header), false);
  }
});
