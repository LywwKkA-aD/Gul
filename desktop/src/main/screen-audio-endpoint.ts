import { stat } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';

/** A Pulse server string may contain a remote fallback list; accept exactly one local endpoint. */
export function localPulseEndpoint(value: unknown): string | null {
  if (typeof value !== 'string' || !value || value.length > 1024) return null;
  const path = value.startsWith('unix:/') ? value.slice(5) : value;
  return isAbsolute(path) && !/[\s:\u0000-\u001f\u007f]/u.test(path) ? `unix:${path}` : null;
}
async function ownSocket(path: string, uid: number): Promise<boolean> {
  try {
    const info = await stat(path);
    return info.isSocket() && info.uid === uid;
  } catch {
    return false;
  }
}
export async function resolvePulseEndpoint(
  environment: Readonly<Record<string, string | undefined>> = process.env,
  uid: number = typeof process.getuid === 'function' ? process.getuid() : -1,
  probe: (path: string, uid: number) => Promise<boolean> = ownSocket,
): Promise<string | null> {
  if (!Number.isSafeInteger(uid) || uid < 0) return null;
  if (environment.PULSE_SERVER !== undefined) {
    const endpoint = localPulseEndpoint(environment.PULSE_SERVER);
    return endpoint && (await probe(endpoint.slice(5), uid)) ? endpoint : null;
  }
  const paths = [
    ...(environment.PULSE_RUNTIME_PATH ? [join(environment.PULSE_RUNTIME_PATH, 'native')] : []),
    ...(environment.XDG_RUNTIME_DIR ? [join(environment.XDG_RUNTIME_DIR, 'pulse', 'native')] : []),
    `/run/user/${uid}/pulse/native`,
  ];
  for (const path of new Set(paths)) {
    const endpoint = localPulseEndpoint(path);
    if (endpoint && (await probe(endpoint.slice(5), uid))) return endpoint;
  }
  return null;
}
