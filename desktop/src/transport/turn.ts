import net from 'node:net';
import type { DialTLS } from './http.ts';
import { readExactly, writeBounded } from './socks.ts';

export function validInitialSTUN(header: Buffer): boolean {
  if (header.length !== 20) return false;
  const type = header.readUInt16BE(0);
  return (
    (type === 1 || type === 3) && header.readUInt32BE(4) === 0x2112a442 && header.readUInt16BE(2) % 4 === 0
  );
}

export async function bridgeTURN(
  socket: net.Socket,
  epoch: number,
  active: () => boolean,
  dial: DialTLS,
): Promise<void> {
  let remote: net.Socket | undefined;
  try {
    const header = await readExactly(socket, 20);
    if (!validInitialSTUN(header) || !active()) throw new Error();
    remote = await dial(epoch);
    if (!active() || socket.destroyed) throw new Error();
    await writeBounded(remote, header);
    socket.once('close', () => remote?.destroy());
    remote.once('close', () => socket.destroy());
    socket.pipe(remote).pipe(socket);
  } catch {
    remote?.destroy();
    socket.destroy();
  }
}
