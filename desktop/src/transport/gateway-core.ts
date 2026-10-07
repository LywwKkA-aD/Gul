import http from 'node:http';
import net from 'node:net';
import type tls from 'node:tls';
import { APP_ORIGIN } from '../shared/contracts.ts';
import { GatewayError, failure } from './errors.ts';
import { brokerRequest, pinnedAgent } from './http.ts';
import { attachSignalServer } from './local-signal.ts';
import { parseRealityProfile } from './profile.ts';
import { EpochTokens } from './tokens.ts';
import { bridgeTURN } from './turn.ts';
import { startXray, type XrayLauncher } from './xray.ts';
import type { Gateway, GatewayOptions } from './gateway.ts';

function listen(server: net.Server): Promise<number> {
  return new Promise((resolve, reject) => {
    server.once('error', () => reject(failure()));
    server.listen(0, '127.0.0.1', () => resolve((server.address() as net.AddressInfo).port));
  });
}
function closeServer(server: net.Server): Promise<void> {
  return new Promise((resolve) => {
    server.close(() => resolve());
  });
}

/** Internal seam for portable child-process fixtures; never exposed over IPC or environment variables. */
export async function createGateway(options: GatewayOptions, launchXray?: XrayLauncher): Promise<Gateway> {
  const profile = parseRealityProfile(options.address);
  if (options.origin !== APP_ORIGIN) throw new GatewayError('profile');
  const tunnel = await startXray(profile, options.password, options.xrayPath, options.ca, launchXray);
  const tokens = new EpochTokens(),
    connections = new Map<net.Socket, number>();
  const signaling = http.createServer({ maxHeaderSize: 8192 }),
    turn = net.createServer();
  signaling.headersTimeout = 3000;
  signaling.requestTimeout = 10000;
  signaling.timeout = 10000;
  signaling.keepAliveTimeout = 30000;
  let closed = false,
    remoteCount = 0,
    signalPort = 0,
    turnPort = 0;
  let stopSignal: () => void = () => undefined;
  let closing: Promise<void> | undefined;
  const check = () => {
    if (closed) throw new GatewayError('closed');
  };
  const track = (socket: net.Socket, epoch: number) => {
    if (closed || (epoch !== 0 && epoch !== tokens.epoch)) {
      socket.destroy();
      throw new GatewayError('stale');
    }
    if (connections.size >= 128) {
      socket.destroy();
      throw failure();
    }
    connections.set(socket, epoch);
    socket.on('error', () => undefined);
    socket.once('close', () => connections.delete(socket));
  };
  const dial = async (epoch: number): Promise<tls.TLSSocket> => {
    check();
    if ((epoch !== 0 && epoch !== tokens.epoch) || remoteCount >= 32) throw failure();
    remoteCount++;
    let released = false;
    const release = () => {
      if (!released) {
        released = true;
        remoteCount--;
      }
    };
    try {
      const secure = await tunnel.dial((socket) => {
        track(socket, epoch);
        socket.once('close', release);
      });
      if (closed || (epoch !== 0 && epoch !== tokens.epoch)) {
        secure.destroy();
        throw failure();
      }
      return secure;
    } catch {
      release();
      throw failure();
    }
  };
  const agent = pinnedAgent(dial);
  const gateway: Gateway = {
    brokerOrigin: profile.origin,
    request<T>(method: 'GET' | 'POST', path: string, token?: string, body?: unknown) {
      try {
        check();
        return brokerRequest<T>(agent, profile.origin, method, path, token, body);
      } catch {
        return Promise.reject(new GatewayError('closed'));
      }
    },
    beginEpoch(epoch) {
      check();
      if (!tokens.begin(epoch)) return;
      for (const [socket, owner] of connections) if (owner !== 0) socket.destroy();
    },
    signalURL(epoch, token) {
      check();
      tokens.register(epoch, token);
      return `ws://127.0.0.1:${signalPort}/${tokens.capability}`;
    },
    close() {
      if (closing) return closing;
      closed = true;
      tokens.clear();
      agent.destroy();
      stopSignal();
      for (const socket of connections.keys()) socket.destroy();
      closing = Promise.all([closeServer(signaling), closeServer(turn), tunnel.close()]).then(
        () => undefined,
      );
      return closing;
    },
  };
  turn.on('connection', (socket) => {
    const epoch = tokens.epoch;
    if (closed || !epoch || connections.size >= 128) {
      socket.destroy();
      return;
    }
    track(socket, epoch);
    void bridgeTURN(socket, epoch, () => !closed && tokens.epoch === epoch, dial);
  });
  signaling.on('connection', (socket) => {
    socket.on('error', () => undefined);
    if (closed || connections.size >= 128) {
      socket.destroy();
      return;
    }
    connections.set(socket, tokens.epoch || -1);
    socket.once('close', () => connections.delete(socket));
  });
  signaling.on('clientError', (_error, socket) => socket.destroy());
  try {
    turnPort = await listen(turn);
    signalPort = await listen(signaling);
    stopSignal = attachSignalServer(signaling, {
      origin: options.origin,
      brokerOrigin: profile.origin,
      host: profile.host,
      turnAddress: `127.0.0.1:${turnPort}`,
      tokens,
      dial,
      authority: () => `127.0.0.1:${signalPort}`,
      trackSocket: track,
    });
    tunnel.onExit(() => {
      void gateway.close();
    });
    return gateway;
  } catch {
    await gateway.close();
    throw failure();
  }
}
