import { createHash } from 'node:crypto';
import { isIP } from 'node:net';
import { GatewayError } from './errors.ts';

export interface RealityProfile {
  readonly address: string;
  readonly host: string;
  readonly port: number;
  readonly origin: string;
  readonly sni: string;
  readonly publicKey: string;
  readonly shortId: string;
}

function dnsName(value: string): boolean {
  return (
    value.length <= 253 &&
    value
      .split('.')
      .every(
        (label) => label.length > 0 && label.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i.test(label),
      )
  );
}

export function parseRealityProfile(raw: string): RealityProfile {
  const invalid = () => new GatewayError('profile');
  try {
    const value = raw.trim();
    if (!/^livekit\+vless:\/\//.test(value) || /[\r\n\t#]/.test(value)) throw invalid();
    const u = new URL(value);
    if (u.username || u.password || u.pathname || !u.hostname || u.hash) throw invalid();
    const host = u.hostname.replace(/^\[|\]$/g, '').toLowerCase();
    const ip = isIP(host);
    if (
      (ip === 0 && (!dnsName(host) || /^[0-9.]+$/.test(host))) ||
      host.includes('%') ||
      host === '0.0.0.0' ||
      host === '::' ||
      (ip === 4 && Number(host.split('.')[0]) >= 224) ||
      (ip === 6 && /^ff/i.test(host))
    )
      throw invalid();
    const authority = value.slice(value.indexOf('//') + 2).split('?')[0];
    if (authority.endsWith(':') || /\s|%/.test(authority)) throw invalid();
    const port = u.port === '' ? 443 : Number(u.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw invalid();
    const q = new Map<string, string>();
    for (const part of u.search.slice(1).split('&')) {
      const at = part.indexOf('=');
      const name = part.slice(0, at),
        v = part.slice(at + 1);
      if (
        at <= 0 ||
        !v ||
        /[%+;\s]/.test(part) ||
        q.has(name) ||
        !['security', 'flow', 'type', 'sni', 'pbk', 'sid'].includes(name)
      )
        throw invalid();
      q.set(name, v);
    }
    if (
      q.size !== 6 ||
      q.get('security') !== 'reality' ||
      q.get('flow') !== 'none' ||
      q.get('type') !== 'tcp'
    )
      throw invalid();
    const sni = q.get('sni')!.toLowerCase(),
      publicKey = q.get('pbk')!,
      shortId = q.get('sid')!;
    const decoded = Buffer.from(publicKey, 'base64url');
    if (
      !dnsName(sni) ||
      isIP(sni) ||
      decoded.length !== 32 ||
      decoded.toString('base64url') !== publicKey ||
      !/^(?:[0-9a-f]{2}){1,8}$/.test(shortId)
    )
      throw invalid();
    q.set('sni', sni);
    const publicHost = ip === 6 ? `[${host}]` : host;
    const params = new URLSearchParams([...q.entries()].sort(([a], [b]) => a.localeCompare(b)));
    return Object.freeze({
      host,
      port,
      origin: `https://${publicHost}`,
      sni,
      publicKey,
      shortId,
      address: `livekit+vless://${publicHost}${port === 443 ? '' : ':' + port}?${params}`,
    });
  } catch {
    throw invalid();
  }
}

/** Exact compatibility with the deployed server's UUIDv8 derivation. */
export function deriveUserID(password: string): string {
  const bytes = createHash('sha256')
    .update('gul/vless-reality/user-id/v1\0')
    .update(password)
    .digest()
    .subarray(0, 16);
  bytes[6] = (bytes[6] & 15) | 128;
  bytes[8] = (bytes[8] & 63) | 128;
  const h = bytes.toString('hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export function xrayConfig(
  profile: RealityProfile,
  password: string,
  port: number,
  user: string,
  pass: string,
) {
  return {
    log: { loglevel: 'none' },
    inbounds: [
      {
        listen: '127.0.0.1',
        port,
        protocol: 'socks',
        settings: { auth: 'password', accounts: [{ user, pass }], udp: false },
      },
    ],
    outbounds: [
      {
        tag: 'reality',
        protocol: 'vless',
        mux: { enabled: false },
        settings: {
          vnext: [
            {
              address: profile.host,
              port: profile.port,
              users: [{ id: deriveUserID(password), encryption: 'none', flow: '' }],
            },
          ],
        },
        streamSettings: {
          network: 'tcp',
          security: 'reality',
          realitySettings: {
            serverName: profile.sni,
            fingerprint: 'chrome',
            password: profile.publicKey,
            shortId: profile.shortId,
          },
        },
      },
      { tag: 'blocked', protocol: 'blackhole' },
    ],
    routing: {
      domainStrategy: 'AsIs',
      rules: [
        { type: 'field', ip: ['127.0.0.1'], port: '443', network: 'tcp', outboundTag: 'reality' },
        { type: 'field', network: 'tcp,udp', outboundTag: 'blocked' },
      ],
    },
  };
}
