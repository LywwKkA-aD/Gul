import { test, expect, _electron as electron } from '@playwright/test';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(!fixture, 'Start the isolated REALITY fixture.');

test('settings scroll inside their frame and the close button stays reachable', async () => {
  const directory = resolve(fixture!);
  const profile = await mkdtemp(join(tmpdir(), 'gul-dialog-e2e-'));
  const caFile = join(directory, 'ca.pem');
  const ca = await access(caFile)
    .then(() => caFile)
    .catch(() => undefined);
  const app = await electron.launch({
    executablePath: require('electron'),
    args: ['.', '--gul-electron-test', '--use-fake-device-for-media-stream', `--user-data-dir=${profile}`],
    env: { ...process.env, NODE_ENV: 'test', ...(ca ? { GUL_ELECTRON_TEST_CA: ca } : {}) },
  });
  try {
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    await privateLoginForm(
      page,
      (await readFile(join(directory, 'address'), 'utf8')).trim(),
      (await readFile(join(directory, 'join-password'), 'utf8')).trim(),
    );
    await page.getByLabel('Твой ник', { exact: true }).fill('dialog-test');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    await app.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()[0].setBounds({ width: 900, height: 560 }),
    );
    await page.getByRole('button', { name: 'Настройки', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Настройки', exact: true });
    await dialog
      .getByText('Звук демонстрации передаётся отдельно от микрофона.', { exact: false })
      .scrollIntoViewIfNeeded();
    const bounds = await dialog.boundingBox();
    const close = dialog.getByRole('button', { name: 'Закрыть: Настройки', exact: true });
    const closeBounds = await close.boundingBox();
    expect(bounds).not.toBeNull();
    expect(closeBounds).not.toBeNull();
    expect(closeBounds!.y).toBeGreaterThanOrEqual(bounds!.y);
    expect(closeBounds!.y + closeBounds!.height).toBeLessThanOrEqual(bounds!.y + bounds!.height);
    await page.screenshot({ path: 'test-results/dialog-scroll.png' });
    await close.click();
    await expect(dialog).toHaveCount(0);
  } finally {
    await app.close();
    await rm(profile, { recursive: true, force: true });
  }
});
