import { ClientConfiguration, ClientConfigSetting, ICEServer, SignalResponse } from '@livekit/protocol';
import { GatewayError } from './errors.ts';

export const MAX_FRAME = 1024 * 1024;

export function trustedTURN(address: string, host: string): boolean {
  const match = /^turns:(\[[0-9a-f:]+\]|[a-z0-9.-]+):443(?:\?transport=tcp)?$/i.exec(address);
  return !!match && match[1].replace(/^\[|\]$/g, '').toLowerCase() === host.toLowerCase();
}

/** Decode only server messages; preserve untouched frames and their unknown fields. */
export function rewriteSignal(
  data: Uint8Array,
  binary: boolean,
  host: string,
  turnAddress: string,
  register: (token: string) => void,
): Uint8Array {
  try {
    if (data.byteLength > MAX_FRAME) throw new Error();
    const message = binary
      ? SignalResponse.fromBinary(data)
      : SignalResponse.fromJsonString(Buffer.from(data).toString('utf8'));
    const kind = message.message.case;
    if (kind === 'refreshToken') {
      const token = message.message.value;
      if (!token || token.length > 16384 || /[\r\n]/.test(token)) throw new Error();
      register(token);
      return data;
    }
    if (kind === 'roomMoved') throw new Error();
    if (kind === 'leave') {
      message.message.value.regions = undefined;
    } else if (kind === 'join' || kind === 'reconnect') {
      const value = message.message.value;
      if ('alternativeUrl' in value && value.alternativeUrl) throw new Error();
      const relay = value.iceServers.find(
        (server) =>
          server.username &&
          server.credential &&
          server.username.length <= 16384 &&
          server.credential.length <= 16384 &&
          server.urls.some((url) => trustedTURN(url, host)),
      );
      if (!relay) throw new Error();
      value.iceServers = [
        new ICEServer({
          urls: [`turn:${turnAddress}?transport=tcp`],
          username: relay.username,
          credential: relay.credential,
        }),
      ];
      value.clientConfiguration = value.clientConfiguration?.clone() ?? new ClientConfiguration();
      value.clientConfiguration.forceRelay = ClientConfigSetting.ENABLED;
    } else {
      return data;
    }
    return binary ? message.toBinary() : Buffer.from(message.toJsonString());
  } catch {
    throw new GatewayError('server');
  }
}
