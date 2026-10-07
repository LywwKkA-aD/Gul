import { readFile } from 'node:fs/promises';
import { extname, join } from 'node:path';
import type { Session } from 'electron';
import { appAsset, contentSecurityPolicy } from './security.ts';

const types: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
};
export function installAppProtocol(session: Session, rendererDirectory: string): void {
  session.protocol.handle('gul', async (request) => {
    const asset = appAsset(request.url);
    if (!asset || !['GET', 'HEAD'].includes(request.method)) return new Response('', { status: 404 });
    try {
      const bytes = await readFile(join(rendererDirectory, asset));
      return new Response(request.method === 'HEAD' ? null : bytes, {
        headers: {
          'Content-Type': types[extname(asset)],
          'Content-Security-Policy': contentSecurityPolicy,
          'X-Content-Type-Options': 'nosniff',
          'Cache-Control': 'no-store',
          'Referrer-Policy': 'no-referrer',
          'Permissions-Policy': 'camera=(), microphone=(self), display-capture=(self)',
        },
      });
    } catch {
      return new Response('', { status: 404 });
    }
  });
}
