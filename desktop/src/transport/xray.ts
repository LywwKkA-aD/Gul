import { spawn, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import net from 'node:net';
import tls from 'node:tls';
import { failure } from './errors.ts';
import { xrayConfig, type RealityProfile } from './profile.ts';
import { authenticateSOCKS, connectFixedTarget, connectLoopback } from './socks.ts';

export interface XrayTunnel {
  dial(track: (socket: net.Socket) => void): Promise<tls.TLSSocket>;
  close(): Promise<void>;
  onExit(callback: () => void): void;
}

export type XrayLauncher = (binary: string, args: readonly string[]) => ChildProcess;
const launchBundledXray: XrayLauncher = (binary, args) =>
  spawn(binary, args, {
    stdio: ['pipe', 'ignore', 'ignore'],
    windowsHide: true,
    shell: false,
  });

async function vacantPort(): Promise<number> {
  const server = net.createServer();
  await new Promise<void>((resolve, reject) => {
    server.once('error', () => reject(failure()));
    server.listen(0, '127.0.0.1', () => resolve());
  });
  const port = (server.address() as net.AddressInfo).port;
  await new Promise<void>((resolve) => server.close(() => resolve()));
  return port;
}

async function stop(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null || !child.pid) return;
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
    }, 1500);
    child.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

export async function startXray(
  profile: RealityProfile,
  password: string,
  binary: string,
  ca?: string | Buffer,
  launch: XrayLauncher = launchBundledXray,
): Promise<XrayTunnel> {
  if (!binary || !password || password.length > 4096) throw failure();
  const port = await vacantPort(),
    user = randomBytes(24).toString('hex'),
    pass = randomBytes(24).toString('hex');
  let alive = true,
    intentional = false;
  let onExit: () => void = () => undefined;
  let child: ChildProcess;
  try {
    child = launch(binary, ['run', '-config', 'stdin:']);
  } catch {
    throw failure();
  }
  child.on('error', () => {
    alive = false;
    onExit();
  });
  child.once('exit', () => {
    alive = false;
    if (!intentional) onExit();
  });
  child.stdin!.on('error', () => undefined);
  child.stdin!.end(JSON.stringify(xrayConfig(profile, password, port, user, pass)));
  try {
    const until = Date.now() + 8000;
    while (true) {
      if (!alive || Date.now() > until) throw failure();
      let probe: net.Socket | undefined;
      try {
        probe = await connectLoopback(port, 250);
        await authenticateSOCKS(probe, user, pass);
        probe.destroy();
        break;
      } catch {
        probe?.destroy();
        await new Promise((resolve) => setTimeout(resolve, 40));
      }
    }
  } catch {
    intentional = true;
    await stop(child);
    throw failure();
  }
  return {
    onExit(callback) {
      onExit = callback;
      if (!alive) callback();
    },
    async dial(track) {
      let socket: net.Socket | undefined;
      try {
        if (!alive) throw failure();
        socket = await connectLoopback(port);
        track(socket);
        await authenticateSOCKS(socket, user, pass);
        await connectFixedTarget(socket);
        const secure = tls.connect({
          socket,
          rejectUnauthorized: true,
          minVersion: 'TLSv1.2',
          ca,
          servername: net.isIP(profile.host) ? undefined : profile.host,
          checkServerIdentity: (_name, certificate) => tls.checkServerIdentity(profile.host, certificate),
        });
        secure.on('error', () => undefined);
        track(secure);
        await new Promise<void>((resolve, reject) => {
          const timer = setTimeout(() => finish(false), 10000);
          const ready = () => finish(true),
            failed = () => finish(false);
          const finish = (ok: boolean) => {
            clearTimeout(timer);
            secure.off('secureConnect', ready);
            secure.off('error', failed);
            secure.off('close', failed);
            ok ? resolve() : reject(failure());
          };
          secure.once('secureConnect', ready);
          secure.once('error', failed);
          secure.once('close', failed);
        });
        return secure;
      } catch {
        socket?.destroy();
        throw failure();
      }
    },
    async close() {
      intentional = true;
      alive = false;
      await stop(child);
    },
  };
}
