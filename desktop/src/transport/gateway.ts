import { createGateway } from './gateway-core.ts';

export interface Gateway {
  readonly brokerOrigin: string;
  request<T>(method: 'GET' | 'POST', path: string, token?: string, body?: unknown): Promise<T>;
  beginEpoch(epoch: number): void;
  signalURL(epoch: number, token: string): string;
  close(): Promise<void>;
}
export interface GatewayOptions {
  readonly address: string;
  readonly password: string;
  readonly origin: string;
  readonly xrayPath: string;
  readonly ca?: string | Buffer;
}

/** Production factory always launches the bundled native Xray binary directly. */
export function createRealityGateway(options: GatewayOptions): Promise<Gateway> {
  return createGateway(options);
}
