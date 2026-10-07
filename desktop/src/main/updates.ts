import { RELEASE_BODY_LIMIT, requestReleaseList } from './updates-network.ts';

interface Version {
  readonly core: readonly bigint[];
  readonly prerelease: readonly string[];
}
function parseVersion(value: string): Version | undefined {
  if (typeof value !== 'string' || value.length > 128) return;
  const parsed =
    /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/u.exec(
      value,
    );
  if (!parsed) return;
  const prerelease = parsed[4]?.split('.') ?? [];
  if (prerelease.some((part) => /^\d+$/u.test(part) && part.length > 1 && part.startsWith('0'))) return;
  return { core: parsed.slice(1, 4).map((part) => BigInt(part)), prerelease };
}
const compare = <T extends string | bigint>(a: T, b: T): -1 | 0 | 1 => (a < b ? -1 : a > b ? 1 : 0);
export function compareVersions(a: string, b: string): -1 | 0 | 1 | undefined {
  const first = parseVersion(a),
    second = parseVersion(b);
  if (!first || !second) return;
  for (let i = 0; i < 3; i++) {
    const result = compare(first.core[i], second.core[i]);
    if (result) return result;
  }
  if (!first.prerelease.length || !second.prerelease.length)
    return first.prerelease.length ? -1 : second.prerelease.length ? 1 : 0;
  for (let i = 0; i < Math.min(first.prerelease.length, second.prerelease.length); i++) {
    const left = first.prerelease[i],
      right = second.prerelease[i];
    const numericLeft = /^\d+$/u.test(left),
      numericRight = /^\d+$/u.test(right);
    const result =
      numericLeft && numericRight
        ? compare(BigInt(left), BigInt(right))
        : numericLeft
          ? -1
          : numericRight
            ? 1
            : compare(left, right);
    if (left !== right && result) return result;
  }
  return compare(BigInt(first.prerelease.length), BigInt(second.prerelease.length));
}
const REPOSITORY = 'https://github.com/LywwKkA-aD/Gul/releases/';
const releaseURL = (tag: string) => `${REPOSITORY}tag/${encodeURIComponent(tag)}`;
export function trustedReleaseURL(value: string): boolean {
  try {
    const url = new URL(value);
    if (
      url.protocol !== 'https:' ||
      url.host !== 'github.com' ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      !url.pathname.startsWith('/LywwKkA-aD/Gul/releases/tag/')
    )
      return false;
    const tag = decodeURIComponent(url.pathname.slice('/LywwKkA-aD/Gul/releases/tag/'.length));
    return Boolean(parseVersion(tag)) && value === releaseURL(tag);
  } catch {
    return false;
  }
}
export interface UpdateAsset {
  readonly name: string;
  readonly url: string;
  readonly size: number;
  readonly sha256: string;
}
export interface UpdateNotice {
  readonly tag: string;
  readonly version: string;
  readonly url: string;
  readonly asset?: UpdateAsset;
}
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
function installer(assets: unknown, tag: string, platform: string, arch: string): UpdateAsset | undefined {
  if (!Array.isArray(assets) || !['x64', 'arm64'].includes(arch)) return;
  const os =
    platform === 'win32' ? 'win' : platform === 'linux' ? 'linux' : platform === 'darwin' ? 'mac' : undefined;
  if (!os || (platform !== 'darwin' && arch !== 'x64')) return;
  const extension = platform === 'win32' ? 'exe' : platform === 'linux' ? 'deb' : 'dmg';
  const name = `Gul-${tag.replace(/^v/u, '')}-${os}-${arch}.${extension}`;
  const url = `${REPOSITORY}download/${encodeURIComponent(tag)}/${name}`;
  const candidate = assets.slice(0, 128).find((asset) => record(asset) && asset.name === name);
  if (
    !record(candidate) ||
    candidate.browser_download_url !== url ||
    candidate.state !== 'uploaded' ||
    typeof candidate.size !== 'number' ||
    !Number.isSafeInteger(candidate.size) ||
    candidate.size <= 0 ||
    candidate.size > 1024 * 1024 * 1024 ||
    typeof candidate.digest !== 'string' ||
    !/^sha256:[a-f0-9]{64}$/u.test(candidate.digest)
  )
    return;
  return Object.freeze({ name, url, size: candidate.size, sha256: candidate.digest.slice(7) });
}

/** Startup notification only. This helper never downloads or executes an installer. */
export async function checkForUpdate(options: {
  readonly current: string;
  readonly dismissed?: string;
  readonly platform?: string;
  readonly arch?: string;
  readonly signal?: AbortSignal;
  readonly request?: (signal: AbortSignal) => Promise<Uint8Array>;
}): Promise<UpdateNotice | null> {
  try {
    if (!parseVersion(options.current) || options.signal?.aborted) return null;
    const signal = options.signal
      ? AbortSignal.any([options.signal, AbortSignal.timeout(5000)])
      : AbortSignal.timeout(5000);
    const bytes = await (options.request ?? requestReleaseList)(signal);
    if (signal.aborted || bytes.byteLength > RELEASE_BODY_LIMIT) return null;
    const releases: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    if (
      !Array.isArray(releases) ||
      !record(releases[0]) ||
      releases[0].draft !== false ||
      typeof releases[0].tag_name !== 'string'
    )
      return null;
    const release = releases[0],
      tag = release.tag_name as string;
    if (
      compareVersions(tag, options.current) !== 1 ||
      (options.dismissed && parseVersion(options.dismissed) && compareVersions(tag, options.dismissed) !== 1)
    )
      return null;
    const asset = installer(
      release.assets,
      tag,
      options.platform ?? process.platform,
      options.arch ?? process.arch,
    );
    return Object.freeze({
      tag,
      version: tag.replace(/^v/u, ''),
      url: releaseURL(tag),
      ...(asset ? { asset } : {}),
    });
  } catch {
    return null;
  }
}
