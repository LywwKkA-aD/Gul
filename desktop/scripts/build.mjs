import { build } from 'esbuild';
import { mkdir, writeFile } from 'node:fs/promises';
import { collectLicenses } from './licenses.mjs';
await mkdir('dist/renderer', { recursive: true });
const bundles = await Promise.all([
  build({
    entryPoints: ['src/main/index.ts'],
    bundle: true,
    metafile: true,
    platform: 'node',
    format: 'cjs',
    target: 'node24',
    external: ['electron'],
    outfile: 'dist/main/index.cjs',
  }),
  build({
    entryPoints: ['src/preload/index.ts'],
    bundle: true,
    metafile: true,
    platform: 'node',
    format: 'cjs',
    target: 'node24',
    external: ['electron'],
    outfile: 'dist/preload/index.cjs',
  }),
  build({
    entryPoints: ['src/renderer/index.tsx'],
    bundle: true,
    metafile: true,
    platform: 'browser',
    format: 'esm',
    target: 'chrome152',
    minify: true,
    outfile: 'dist/renderer/app.js',
    loader: { '.woff2': 'file', '.woff': 'file' },
    define: { 'process.env.NODE_ENV': '"production"' },
  }),
]);
await collectLicenses(bundles);
await writeFile(
  'dist/renderer/index.html',
  `<!doctype html>
<html lang="ru"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src ws://127.0.0.1:* http://127.0.0.1:*; object-src 'none'; base-uri 'none'; form-action 'none'">
<title>Gul</title><link rel="stylesheet" href="app.css"></head><body><div id="root"></div><script type="module" src="app.js"></script></body></html>`,
);
