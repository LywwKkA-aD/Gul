import net from 'node:net';
import { failure } from './errors.ts';

/** Read without consuming bytes from the following TLS/STUN frame. */
export function readExactly(socket: net.Socket, size: number, timeout = 5000): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => finish(), timeout);
    const clean = () => {
      clearTimeout(timer);
      socket.off('readable', readable);
      socket.off('error', failed);
      socket.off('end', failed);
      socket.off('close', failed);
    };
    const finish = (data?: Buffer) => {
      clean();
      data ? resolve(data) : reject(failure());
    };
    const failed = () => finish();
    const readable = () => {
      const data = socket.read(size) as Buffer | null;
      if (data) finish(data.length === size ? data : undefined);
    };
    socket.on('readable', readable);
    socket.once('error', failed);
    socket.once('end', failed);
    socket.once('close', failed);
    readable();
    if (socket.destroyed) failed();
  });
}

export function writeBounded(socket: net.Socket, data: Uint8Array, timeout = 5000): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      socket.destroy();
      reject(failure());
    }, timeout);
    socket.write(data, (error) => {
      clearTimeout(timer);
      error ? reject(failure()) : resolve();
    });
  });
}

export async function authenticateSOCKS(socket: net.Socket, user: string, password: string): Promise<void> {
  await writeBounded(socket, Buffer.from([5, 1, 2]));
  if (!(await readExactly(socket, 2)).equals(Buffer.from([5, 2]))) throw failure();
  const u = Buffer.from(user),
    p = Buffer.from(password);
  if (!u.length || u.length > 255 || !p.length || p.length > 255) throw failure();
  await writeBounded(socket, Buffer.concat([Buffer.from([1, u.length]), u, Buffer.from([p.length]), p]));
  if (!(await readExactly(socket, 2)).equals(Buffer.from([1, 0]))) throw failure();
}

/** Destination is intentionally not an argument. There is no direct network fallback. */
export async function connectFixedTarget(socket: net.Socket): Promise<void> {
  await writeBounded(socket, Buffer.from([5, 1, 0, 1, 127, 0, 0, 1, 1, 187]));
  const response = await readExactly(socket, 4);
  if (response[0] !== 5 || response[1] !== 0 || response[2] !== 0) throw failure();
  const length =
    response[3] === 1
      ? 4
      : response[3] === 4
        ? 16
        : response[3] === 3
          ? (await readExactly(socket, 1))[0]
          : 0;
  if (!length) throw failure();
  await readExactly(socket, length + 2);
}

export async function connectLoopback(port: number, timeout = 5000): Promise<net.Socket> {
  const socket = net.connect({ host: '127.0.0.1', port });
  socket.on('error', () => undefined);
  try {
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => finish(false), timeout);
      const connected = () => finish(true),
        failed = () => finish(false);
      const finish = (ok: boolean) => {
        clearTimeout(timer);
        socket.off('connect', connected);
        socket.off('error', failed);
        socket.off('close', failed);
        ok ? resolve() : reject(failure());
      };
      socket.once('connect', connected);
      socket.once('error', failed);
      socket.once('close', failed);
    });
    socket.setNoDelay(true);
    return socket;
  } catch {
    socket.destroy();
    throw failure();
  }
}
