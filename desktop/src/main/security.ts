import { APP_ORIGIN } from '../shared/contracts.ts';

export const contentSecurityPolicy = [
  "default-src 'none'",
  "script-src 'self' 'wasm-unsafe-eval'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  'connect-src ws://127.0.0.1:* http://127.0.0.1:*',
  "media-src 'self' blob:",
  "worker-src 'self' blob:",
  "object-src 'none'",
  "frame-src 'none'",
  "frame-ancestors 'none'",
  "base-uri 'none'",
  "form-action 'none'",
].join('; ');

function appURL(value: unknown): URL | undefined {
  if (typeof value !== 'string') return;
  try {
    const url = new URL(value);
    if (
      url.protocol === 'gul:' &&
      url.hostname === 'app' &&
      !url.port &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash
    )
      return url;
  } catch {
    /* Untrusted URLs are denied. */
  }
}
export function appPage(value: unknown): boolean {
  const url = appURL(value);
  return Boolean(url && (url.pathname === '/' || url.pathname === '/index.html' || url.pathname === ''));
}
/** Electron uses both Origin.serialize() and GURL.spec() for the same registered origin. */
export function appOrigin(value: unknown): boolean {
  return value === APP_ORIGIN || value === `${APP_ORIGIN}/`;
}
export function appAsset(value: unknown): string | null {
  const url = appURL(value);
  if (!url) return null;
  let pathname: string;
  try {
    pathname = decodeURIComponent(url.pathname);
  } catch {
    return null;
  }
  if (pathname === '/' || pathname === '') return 'index.html';
  const asset = pathname.slice(1);
  if (
    !/^(?:[a-zA-Z0-9_-]+\/)*[a-zA-Z0-9_.-]+\.(?:html|js|css|png|svg|ico|woff2)$/u.test(asset) ||
    asset.includes('..') ||
    (asset.endsWith('.html') && asset !== 'index.html')
  )
    return null;
  return asset;
}
export interface PermissionDetails {
  readonly requestingUrl?: string;
  readonly isMainFrame: boolean;
  readonly mediaType?: string;
  readonly mediaTypes?: readonly string[];
  readonly securityOrigin?: string;
}
export function mediaPermission(
  permission: string,
  details: PermissionDetails,
  ownedContents: boolean,
): boolean {
  if (!ownedContents || !details.isMainFrame || !appPage(details.requestingUrl) || permission !== 'media')
    return false;
  return (
    details.mediaType === 'audio' || (details.mediaTypes?.length === 1 && details.mediaTypes[0] === 'audio')
  );
}
/**
 * Electron44 dispatches getDisplayMedia as `media` with no DEVICE_* types.
 * This is preflight only: the display handler still requires gesture, epoch and source consent.
 * The all-frame device-only getUserMedia guard blocks the legacy desktop capture path.
 */
export function displayMediaPreflight(
  permission: string,
  details: PermissionDetails,
  ownedContents: boolean,
  activeSession: boolean,
  guardReady: boolean,
): boolean {
  return (
    ownedContents &&
    activeSession &&
    guardReady &&
    permission === 'media' &&
    details.isMainFrame &&
    appPage(details.requestingUrl) &&
    appOrigin(details.securityOrigin) &&
    details.mediaType === undefined &&
    Array.isArray(details.mediaTypes) &&
    details.mediaTypes.length === 0
  );
}
interface CaptureRequest {
  readonly securityOrigin: string;
  readonly videoRequested: boolean;
  readonly userGesture: boolean;
}
export function captureAllowed(
  request: CaptureRequest,
  frameURL: string | undefined,
  sameMainFrame: boolean,
): boolean {
  return (
    sameMainFrame &&
    appPage(frameURL) &&
    appOrigin(request.securityOrigin) &&
    request.videoRequested &&
    request.userGesture
  );
}
const paths = new Set(['/rtc', '/rtc/v1', '/rtc/validate', '/rtc/v1/validate']);
export function allowedNetwork(value: string, endpoints: readonly string[]): boolean {
  if (appAsset(value)) return true;
  try {
    const request = new URL(value);
    if (
      !['ws:', 'http:'].includes(request.protocol) ||
      request.hostname !== '127.0.0.1' ||
      request.username ||
      request.password ||
      request.hash
    )
      return false;
    return endpoints.some((endpoint) => {
      const base = new URL(endpoint);
      return (
        base.hostname === '127.0.0.1' &&
        request.host === base.host &&
        request.pathname.startsWith(base.pathname) &&
        paths.has(request.pathname.slice(base.pathname.length))
      );
    });
  } catch {
    return false;
  }
}
export function testOptions(
  env: NodeJS.ProcessEnv,
  packaged: boolean,
  args: readonly string[],
): { caFile?: string; xrayPath?: string } {
  if (packaged || env.NODE_ENV !== 'test' || !args.includes('--gul-electron-test')) return {};
  return {
    ...(env.GUL_ELECTRON_TEST_CA ? { caFile: env.GUL_ELECTRON_TEST_CA } : {}),
    ...(env.GUL_ELECTRON_TEST_XRAY ? { xrayPath: env.GUL_ELECTRON_TEST_XRAY } : {}),
  };
}
