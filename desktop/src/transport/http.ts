import http from 'node:http';
import https from 'node:https';
import type tls from 'node:tls';
import { GatewayError, failure } from './errors.ts';
import { validToken } from './tokens.ts';

export type DialTLS = (epoch: number) => Promise<tls.TLSSocket>;

export function pinnedAgent(dial: DialTLS): https.Agent {
  const agent = new https.Agent({ keepAlive: true, maxSockets: 8, maxFreeSockets: 2, timeout: 30000 });
  agent.createConnection = ((
    _options: unknown,
    callback: (error: Error | null, socket?: tls.TLSSocket) => void,
  ) => {
    void dial(0).then(
      (socket) => callback(null, socket),
      () => callback(failure()),
    );
    return undefined;
  }) as typeof agent.createConnection;
  return agent;
}

const methods = new Map([
  ['/healthz', 'GET'],
  ['/api/gul/login', 'POST'],
  ['/api/gul/state', 'GET'],
  ['/api/gul/channel', 'POST'],
  ['/api/gul/audio', 'POST'],
  ['/api/gul/screen', 'POST'],
  ['/api/gul/logout', 'POST'],
  ['/api/gul/info', 'GET'],
  ['/api/gul/members', 'GET'],
  ['/api/gul/channels/create', 'POST'],
  ['/api/gul/channels/update', 'POST'],
  ['/api/gul/channels/delete', 'POST'],
  ['/api/gul/channels/permissions', 'POST'],
  ['/api/gul/invites/create', 'POST'],
  ['/api/gul/invites/redeem', 'POST'],
]);

/** Match fixed server codes to their status; exception bodies never become error messages. */
export function brokerError(status: number, value: unknown): GatewayError {
  const code =
    value && typeof value === 'object' && !Array.isArray(value)
      ? (value as Record<string, unknown>).code
      : undefined;
  if (status === 404) return new GatewayError('not-found');
  if (status === 403 && code === 'owner_required') return new GatewayError('owner-required');
  if (status === 403 && code === 'access_denied') return new GatewayError('access-denied');
  if (status === 409 && code === 'channel_busy') return new GatewayError('channel-busy');
  if (status === 426 && code === 'upgrade_required') return new GatewayError('upgrade-required');
  if (status === 503 && code === 'media_cleanup_pending') return new GatewayError('cleanup-pending');
  if (status === 503 && code === 'storage_unavailable') return new GatewayError('storage-unavailable');
  return new GatewayError(
    status === 401 || status === 403 ? 'authentication' : status === 409 ? 'stale' : 'server',
  );
}

export function brokerRequest<T>(
  agent: https.Agent,
  origin: string,
  method: 'GET' | 'POST',
  path: string,
  token?: string,
  body?: unknown,
): Promise<T> {
  if (methods.get(path) !== method || (token !== undefined && !validToken(token)))
    return Promise.reject(new GatewayError('server'));
  let encoded: string | undefined;
  try {
    encoded = body === undefined ? undefined : JSON.stringify(body);
    if (encoded !== undefined && Buffer.byteLength(encoded) > 65536) throw new Error();
  } catch {
    return Promise.reject(new GatewayError('server'));
  }
  return new Promise((resolve, reject) => {
    let finished = false;
    const timer = setTimeout(() => {
      request.destroy();
      finish(failure());
    }, 15000);
    const finish = (error?: GatewayError, value?: T) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      error ? reject(error) : resolve(value as T);
    };
    const request = https.request(
      origin + path,
      {
        method,
        agent,
        headers: {
          'Content-Type': 'application/json',
          ...(token === undefined ? {} : { Authorization: `Bearer ${token}` }),
          ...(encoded === undefined ? {} : { 'Content-Length': Buffer.byteLength(encoded) }),
        },
      },
      (response) => {
        const status = response.statusCode ?? 0;
        const error = status < 200 || status >= 300;
        const chunks: Buffer[] = [];
        let size = 0;
        response.on('data', (chunk: Buffer) => {
          size += chunk.length;
          if (size > (error ? 1024 : 1024 * 1024)) {
            finish(new GatewayError('server'));
            response.destroy();
          } else chunks.push(chunk);
        });
        response.on('error', () => finish(failure()));
        response.on('aborted', () => finish(failure()));
        response.on('end', () => {
          try {
            const data = Buffer.concat(chunks).toString('utf8');
            if (error) {
              let value: unknown;
              try {
                value = data ? JSON.parse(data) : undefined;
              } catch {
                /* Non-JSON errors retain only their status. */
              }
              finish(brokerError(status, value));
            } else finish(undefined, data ? (JSON.parse(data) as T) : undefined);
          } catch {
            finish(new GatewayError('server'));
          }
        });
      },
    );
    request.on('error', () => finish(failure()));
    request.end(encoded);
  });
}

export async function validateSignal(
  dial: DialTLS,
  epoch: number,
  origin: string,
  path: string,
  token: string,
): Promise<Buffer> {
  const socket = await dial(epoch);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      socket.destroy();
      reject(failure());
    }, 10000);
    const request = http.request(
      {
        host: new URL(origin).host,
        path,
        method: 'GET',
        createConnection: () => socket,
        headers: { Authorization: `Bearer ${token}`, Host: new URL(origin).host },
      },
      (response) => {
        const chunks: Buffer[] = [];
        let size = 0;
        if (!response.statusCode || response.statusCode < 200 || response.statusCode >= 300) {
          request.destroy();
          finish();
          return;
        }
        response.on('data', (chunk: Buffer) => {
          size += chunk.length;
          if (size > 4096) {
            request.destroy();
            finish();
          } else chunks.push(chunk);
        });
        response.on('end', () => finish(Buffer.concat(chunks)));
        response.on('error', () => finish());
        response.on('aborted', () => finish());
      },
    );
    const finish = (data?: Buffer) => {
      clearTimeout(timer);
      socket.destroy();
      data ? resolve(data) : reject(failure());
    };
    request.on('error', () => finish());
    request.end();
  });
}
