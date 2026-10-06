import { readFileSync, statSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { parseEnv } from 'node:util';

export function loadRemoteConfig(path: string | undefined): { url: string; password: string } {
  if (!path || !isAbsolute(path)) throw new Error('Set GUL_REMOTE_E2E_ENV to an absolute private env-file path');
  let text: string;
  try {
    if ((statSync(path).mode & 0o077) !== 0) throw new Error('permissions');
    text = readFileSync(path, 'utf8');
  } catch {
    throw new Error('Remote E2E env file must be private (mode 0600)');
  }
  try {
    const env = parseEnv(text);
    const url = new URL(env.GUL_REMOTE_URL ?? '');
    const password = env.GUL_REMOTE_PASSWORD ?? '';
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash ||
        (url.pathname !== '' && url.pathname !== '/') || Buffer.byteLength(password) < 16) throw new Error();
    return { url: url.origin, password };
  } catch {
    throw new Error('Remote E2E requires a clean HTTPS origin and a server password of at least 16 bytes');
  }
}
