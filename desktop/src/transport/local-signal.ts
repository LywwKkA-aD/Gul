import type http from 'node:http';
import type net from 'node:net';
import { WebSocket, WebSocketServer } from 'ws';
import type { DialTLS } from './http.ts';
import { validateSignal } from './http.ts';
import { MAX_FRAME, rewriteSignal } from './signal.ts';
import type { EpochTokens } from './tokens.ts';

export interface SignalContext {
  readonly origin: string;
  readonly brokerOrigin: string;
  readonly host: string;
  readonly turnAddress: string;
  readonly tokens: EpochTokens;
  readonly dial: DialTLS;
  authority(): string;
  trackSocket(socket: net.Socket, epoch: number): void;
}

function oneHeader(request: http.IncomingMessage, name: string): string | undefined {
  const entries = request.rawHeaders.filter(
    (_value, index) => index % 2 === 0 && request.rawHeaders[index].toLowerCase() === name,
  );
  if (entries.length !== 1) return undefined;
  const value = request.headers[name];
  return typeof value === 'string' ? value : undefined;
}

function authorize(request: http.IncomingMessage, context: SignalContext) {
  if (
    request.method !== 'GET' ||
    !request.url ||
    request.url.length > 32768 ||
    oneHeader(request, 'host') !== context.authority() ||
    oneHeader(request, 'origin') !== context.origin ||
    !context.tokens.epoch
  )
    throw new Error();
  const url = new URL(request.url, 'http://loopback');
  const allowed = ['rtc', 'rtc/validate', 'rtc/v1', 'rtc/v1/validate'].map(
    (path) => `/${context.tokens.capability}/${path}`,
  );
  if (!allowed.includes(url.pathname) || request.url.split('?')[0] !== url.pathname || url.hash)
    throw new Error();
  const query = url.searchParams.getAll('access_token');
  const auth = oneHeader(request, 'authorization');
  const token = query.length === 1 ? query[0] : auth?.startsWith('Bearer ') ? auth.slice(7) : '';
  if (
    query.length > 1 ||
    (auth !== undefined && auth !== `Bearer ${token}`) ||
    !context.tokens.accepts(token)
  )
    throw new Error();
  if (
    request.rawHeaders.filter(
      (_v, i) => i % 2 === 0 && request.rawHeaders[i].toLowerCase() === 'authorization',
    ).length > 1
  )
    throw new Error();
  url.searchParams.delete('access_token');
  return {
    token,
    epoch: context.tokens.epoch,
    path: url.pathname.slice(context.tokens.capability.length + 1) + url.search,
  };
}

const deny = (socket: net.Socket) =>
  socket.end('HTTP/1.1 403 Forbidden\r\nConnection: close\r\nContent-Length: 0\r\n\r\n');

export function attachSignalServer(server: http.Server, context: SignalContext): () => void {
  const wsServer = new WebSocketServer({ noServer: true, maxPayload: MAX_FRAME, perMessageDeflate: false });
  const sessions = new Set<WebSocket>();
  server.on('request', (request, response) => {
    void (async () => {
      try {
        const authorized = authorize(request, context);
        if (!authorized.path.split('?')[0].endsWith('/validate')) throw new Error();
        const data = await validateSignal(
          context.dial,
          authorized.epoch,
          context.brokerOrigin,
          authorized.path,
          authorized.token,
        );
        if (authorized.epoch !== context.tokens.epoch) throw new Error();
        response.writeHead(200, {
          'Content-Type': 'text/plain',
          'Access-Control-Allow-Origin': context.origin,
          'Cache-Control': 'no-store',
        });
        response.end(data);
      } catch {
        response.writeHead(403, { 'Cache-Control': 'no-store' });
        response.end();
      }
    })();
  });
  server.on('upgrade', (request, duplex, head) => {
    const socket = duplex as net.Socket;
    void (async () => {
      let remote: WebSocket | undefined;
      try {
        const authorized = authorize(request, context);
        if (authorized.path.split('?')[0].endsWith('/validate')) throw new Error();
        context.trackSocket(socket, authorized.epoch);
        const secure = await context.dial(authorized.epoch);
        if (authorized.epoch !== context.tokens.epoch || socket.destroyed) {
          secure.destroy();
          throw new Error();
        }
        const url = context.brokerOrigin.replace('https:', 'wss:') + authorized.path;
        remote = new WebSocket(url, {
          createConnection: () => secure,
          followRedirects: false,
          handshakeTimeout: 10000,
          maxPayload: MAX_FRAME,
          perMessageDeflate: false,
          headers: { Authorization: `Bearer ${authorized.token}` },
        });
        remote.on('error', () => {
          socket.destroy();
          remote?.terminate();
        });
        remote.on('unexpected-response', (_request, response) => {
          response.destroy();
          socket.destroy();
          remote?.terminate();
        });
        let previous = authorized.token;
        // Register immediately: the upstream may send Join in the same read as its handshake.
        const pending: { data: Buffer; binary: boolean }[] = [];
        let local: WebSocket | undefined,
          queued = 0;
        const terminate = () => {
          local?.terminate();
          remote?.terminate();
          socket.destroy();
        };
        const send = (destination: WebSocket, data: Uint8Array, binary: boolean) => {
          if (
            destination.readyState !== WebSocket.OPEN ||
            destination.bufferedAmount + data.byteLength > MAX_FRAME
          ) {
            terminate();
            return;
          }
          const timer = setTimeout(terminate, 10000);
          destination.send(data, { binary }, (error) => {
            clearTimeout(timer);
            if (error) terminate();
          });
        };
        remote.on('message', (data, binary) => {
          try {
            if (authorized.epoch !== context.tokens.epoch) throw new Error();
            const bytes = Buffer.isBuffer(data)
              ? data
              : Array.isArray(data)
                ? Buffer.concat(data)
                : Buffer.from(data);
            const rewritten = rewriteSignal(bytes, binary, context.host, context.turnAddress, (token) => {
              context.tokens.refresh(authorized.epoch, previous, token);
              previous = token;
            });
            if (local) send(local, rewritten, binary);
            else {
              queued += rewritten.byteLength;
              if (queued > MAX_FRAME) throw new Error();
              pending.push({ data: Buffer.from(rewritten), binary });
            }
          } catch {
            terminate();
          }
        });
        remote.once('close', terminate);
        await new Promise<void>((resolve, reject) => {
          remote!.once('open', () => resolve());
          remote!.once('error', () => reject(new Error()));
          remote!.once('close', () => reject(new Error()));
        });
        if (socket.destroyed || authorized.epoch !== context.tokens.epoch) throw new Error();
        wsServer.handleUpgrade(request, socket, head, (ws) => {
          local = ws;
          sessions.add(ws);
          ws.on('error', terminate);
          ws.once('close', () => {
            sessions.delete(ws);
            terminate();
          });
          ws.on('message', (data, binary) => {
            const bytes = Buffer.isBuffer(data)
              ? data
              : Array.isArray(data)
                ? Buffer.concat(data)
                : Buffer.from(data);
            if (authorized.epoch !== context.tokens.epoch || bytes.length > MAX_FRAME) terminate();
            else send(remote!, bytes, binary);
          });
          for (const item of pending) send(ws, item.data, item.binary);
        });
      } catch {
        remote?.terminate();
        if (!socket.destroyed) deny(socket);
      }
    })();
  });
  return () => {
    for (const ws of sessions) ws.terminate();
    wsServer.close();
  };
}
