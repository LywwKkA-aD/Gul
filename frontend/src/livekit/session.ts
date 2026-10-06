import type { ConnectionStatus } from '../state/types';
import type { JoinGrant } from './controller';

export interface ScreenSession {
  readonly epoch: number;
  readonly channelId: number;
  readonly serverOrigin: string;
  readonly key: string;
}

export interface ScreenGrant extends JoinGrant {
  ownerIdentity: string;
  channelId: number;
  epoch: number;
}

/** Only the backend-confirmed channel may own capture; navigation state is insufficient. */
export function screenSession(status: ConnectionStatus): ScreenSession | null {
  const { epoch, selfChannel } = status;
  if (status.state !== 'connected' || epoch === undefined || selfChannel === undefined ||
      !Number.isSafeInteger(epoch) || epoch <= 0 || !Number.isInteger(selfChannel) ||
      selfChannel < 0 || selfChannel > 0xffffffff) return null;
  try {
    const server = new URL(status.server);
    if (!cleanRoot(server) || (server.protocol !== 'https:' && server.origin !== 'http://127.0.0.1:8787')) return null;
    const serverOrigin = server.origin;
    return Object.freeze({ epoch, channelId: selfChannel, serverOrigin, key: `${serverOrigin}|${epoch}:${selfChannel}` });
  } catch {
    return null;
  }
}

function cleanRoot(url: URL): boolean {
  return !url.username && !url.password && !url.search && !url.hash && (url.pathname === '' || url.pathname === '/');
}

function trustedEndpoint(endpoint: URL, serverOrigin: string): boolean {
  if (!cleanRoot(endpoint)) return false;
  if (serverOrigin === 'http://127.0.0.1:8787') return endpoint.origin === 'ws://127.0.0.1:7880';
  const server = new URL(serverOrigin);
  return server.protocol === 'https:' && endpoint.protocol === 'wss:' && endpoint.host === server.host;
}

/** The companion identity and token come only from the current Go session. */
export function sessionGrantProvider(
  session: ScreenSession,
  request: (epoch: number, channelId: number) => Promise<ScreenGrant>,
): () => Promise<ScreenGrant> {
  return async () => {
    try {
      const grant = await request(session.epoch, session.channelId);
      const endpoint = new URL(grant.url);
      if (grant.epoch !== session.epoch || grant.channelId !== session.channelId ||
          !grant.token || !grant.identity || !grant.ownerIdentity || !grant.room ||
          grant.identity === grant.ownerIdentity ||
          !trustedEndpoint(endpoint, session.serverOrigin)) throw new Error('Invalid screen grant');
      return grant;
    } catch {
      // Wails/network exceptions may carry authentication response details.
      throw new Error('Screen session is no longer available');
    }
  };
}
