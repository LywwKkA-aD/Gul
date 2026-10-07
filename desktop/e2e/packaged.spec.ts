import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, rm, readFile, access } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const executablePath = process.env.GUL_PACKAGED_APP_PATH;
test.skip(!executablePath, 'Set GUL_PACKAGED_APP_PATH to the packaged executable.');

test('packaged app loads the sandboxed UI and includes the verified native Xray', async () => {
  const profile = await mkdtemp(join(tmpdir(), 'gul-packaged-e2e-'));
  const app = await electron.launch({ executablePath, args: [`--user-data-dir=${profile}`] });
  try {
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

    const licenses = JSON.parse(
      await readFile(join(resourceRoot, 'THIRD_PARTY_LICENSES', 'manifest.json'), 'utf8'),
    ) as { name: string }[];
    expect(licenses.map(({ name }) => name)).toEqual(
      expect.arrayContaining([
        'livekit-client',
        'react',
        '@fontsource/ibm-plex-sans',
        '@fontsource/martian-mono',
      ]),
    );
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    const boundary = await page.evaluate(() => ({
      node: typeof (window as unknown as { require?: unknown }).require,
      api: typeof window.gul?.connect,
    }));
    expect(boundary).toEqual({ node: 'undefined', api: 'function' });
    if (process.platform === 'win32') {
      await page.evaluate(() => window.gul.setPushToTalk('F8', 'hold'));
      await page.evaluate(() => window.gul.setPushToTalk(null));
    }

    await page.screenshot({ path: 'test-results/desktop-packaged.png' });
  } finally {
    await app.close();
    await rm(profile, { recursive: true, force: true });
  }
});
