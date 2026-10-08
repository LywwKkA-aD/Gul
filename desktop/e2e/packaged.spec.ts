import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, rm, readFile, access } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { execFile } from 'node:child_process';
import { constants } from 'node:fs';
import { promisify } from 'node:util';
import { installPackagedStartupDiagnostics, readPackagedStartup } from './packaged-startup.ts';

const executablePath = process.env.GUL_PACKAGED_APP_PATH;
test.skip(!executablePath, 'Set GUL_PACKAGED_APP_PATH to the packaged executable.');

test('packaged app loads the sandboxed UI and includes the verified native Xray', async () => {
  const profile = await mkdtemp(join(tmpdir(), 'gul-packaged-e2e-'));
  const app = await electron.launch({ executablePath, args: [`--user-data-dir=${profile}`] });
  let appClosed = false;
  let processExit: { code: number | null; signal: NodeJS.Signals | null } | null = null;
  let startup: ReturnType<typeof readPackagedStartup> = { ready: false, windows: [], events: [] };
  app.process().once('exit', (code, signal) => {
    processExit = { code, signal };
  });
  app.on('close', () => {
    appClosed = true;
  });
  try {
    await app.evaluate(installPackagedStartupDiagnostics);
    const resources = await app.evaluate(({ app }) => ({ packaged: app.isPackaged, path: app.getAppPath() }));
    expect(resources.packaged).toBe(true);
    const folder = join(dirname(resources.path), 'xray', `${process.platform}-${process.arch}`);
    const binary = join(folder, process.platform === 'win32' ? 'xray.exe' : 'xray');
    await access(binary);
    const checksum = createHash('sha256')
      .update(await readFile(binary))
      .digest('hex');
    expect(checksum).toBe((await readFile(join(folder, 'SHA256'), 'utf8')).trim());
    await access(join(folder, 'LICENSE'));
    const version = await promisify(execFile)(binary, ['version'], { timeout: 10_000 });
    expect(version.stdout).toContain('Xray 26.3.27');
    const resourceRoot = dirname(resources.path);
    await access(join(resourceRoot, 'LICENSE'));
    await access(join(resourceRoot, 'NOTICE'));
    if (process.platform === 'win32' || process.platform === 'linux')
      await access(
        join(
          resourceRoot,
          'ptt',
          `${process.platform}-${process.arch}`,
          process.platform === 'win32' ? 'gul-ptt.exe' : 'gul-ptt',
        ),
      );
    if (process.platform === 'linux') {
      const helper = join(resourceRoot, 'audio-capture', 'linux-x64', 'gul-audio');
      await access(helper, constants.X_OK);
      // Invalid argc exits before touching PulseAudio, while still proving ELF dependencies load.
      const code = await promisify(execFile)(helper, [], { timeout: 5000, maxBuffer: 16 * 1024 }).then(
        () => 0,
        (error: unknown) => {
          const code = error && typeof error === 'object' && 'code' in error ? error.code : undefined;
          return typeof code === 'number' ? code : -1;
        },
      );
      expect(code).toBe(1);
      const passwordStore = join(resourceRoot, 'password-store', 'linux-x64', 'gul-password-store');
      await access(passwordStore, constants.X_OK);
      // Missing arguments must terminate before contacting the user's D-Bus session.
      const passwordStoreCode = await promisify(execFile)(passwordStore, [], {
        timeout: 5000,
        maxBuffer: 128,
      }).then(
        () => 0,
        (error: unknown) =>
          error && typeof error === 'object' && 'code' in error && typeof error.code === 'number'
            ? error.code
            : -1,
      );
      expect(passwordStoreCode).toBe(64);
    }
    if (process.platform === 'win32') {
      const helper = join(resourceRoot, 'audio-capture', 'win32-x64', 'gul-audio.exe');
      await access(helper);
      const code = await promisify(execFile)(helper, [], { timeout: 5000, maxBuffer: 16 * 1024 }).then(
        () => 0,
        (error: unknown) =>
          error && typeof error === 'object' && 'code' in error && typeof error.code === 'number'
            ? error.code
            : -1,
      );
      expect(code).toBe(1);
    }

    const licenses = JSON.parse(
      await readFile(join(resourceRoot, 'THIRD_PARTY_LICENSES', 'manifest.json'), 'utf8'),
    ) as { name: string }[];
    expect(licenses.map(({ name }) => name)).toEqual(
      expect.arrayContaining([
        'livekit-client',
        '@jitsi/rnnoise-wasm',
        'react',
        '@fontsource/ibm-plex-sans',
        '@fontsource/martian-mono',
      ]),
    );
    await access(
      join(
        resourceRoot,
        'THIRD_PARTY_LICENSES',
        'npm',
        '@jitsi',
        'rnnoise-wasm',
        '0.2.1',
        'LICENSE-RNNOISE-BSD-3-Clause',
      ),
    );
    // firstWindow() also returns the hidden startup about:blank Page. Wait in the
    // main process until the final app frame is loaded before touching its CDP context.
    await expect
      .poll(
        async () => {
          startup = await app.evaluate(readPackagedStartup);
          return startup.ready;
        },
        { timeout: 15_000 },
      )
      .toBe(true);
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    const logo = page.getByRole('img', { name: 'Gul', exact: true });
    await expect(logo).toBeVisible();
    await expect
      .poll(() => logo.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth === 1024))
      .toBe(true);
    const boundary = await page.evaluate(() => ({
      node: typeof (window as unknown as { require?: unknown }).require,
      api: typeof window.gul?.connect,
    }));
    expect(boundary).toEqual({ node: 'undefined', api: 'function' });
    if (process.platform === 'win32') {
      const capabilities = await page.evaluate(() => window.gul.captureCapabilities());
      expect(capabilities.platform).toBe('win32');
      expect(capabilities.ownAudioExcluded).toBe(capabilities.systemAudio);
      await page.evaluate(() => window.gul.setPushToTalk('F8', 'hold'));
      await page.evaluate(() => window.gul.setPushToTalk(null));
    }

    await page.screenshot({ path: 'test-results/desktop-packaged.png' });
  } catch (error) {
    try {
      startup = await app.evaluate(readPackagedStartup);
    } catch {
      /* Preserve the last bounded snapshot if the app exited. */
    }
    console.info('GUL_PACKAGED_STARTUP', JSON.stringify({ appClosed, processExit, ...startup }));
    throw error;
  } finally {
    await app.close();
    await rm(profile, { recursive: true, force: true });
  }
});
