import type { ConnectionStatus } from '../state/types';
import type { JoinGrant } from './controller';

export interface ScreenSession {
  readonly epoch: number;
  readonly channelId: number;
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
  return Object.freeze({ epoch, channelId: selfChannel, key: `${epoch}:${selfChannel}` });
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
          !['ws:', 'wss:'].includes(endpoint.protocol) || endpoint.username || endpoint.password ||
          endpoint.search || endpoint.hash) throw new Error('Invalid screen grant');
      return grant;
    } catch {
      // Wails/network exceptions may carry authentication response details.
      throw new Error('Screen session is no longer available');
    }
  };
}
