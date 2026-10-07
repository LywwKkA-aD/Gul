import { test, expect, _electron as electron } from '@playwright/test';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(!fixture, 'Start the isolated REALITY fixture.');
test.skip(
  process.platform !== 'linux' || process.env.GUL_ELECTRON_ISOLATED_DESKTOP !== '1',
  'Native source publication runs only in an explicitly isolated Linux desktop with synthetic data.',
);

test('native source requests use app cards, explicit selection and cancellation', async () => {
  const directory = resolve(fixture!);
  const profile = await mkdtemp(join(tmpdir(), 'gul-picker-ui-'));
  const caFile = join(directory, 'ca.pem');
  const ca = await access(caFile)
    .then(() => caFile)
    .catch(() => undefined);
  const environment = Object.fromEntries(
    Object.entries(process.env).filter(([key]) => key !== 'GUL_ELECTRON_TEST_CAPTURE_APPROVED'),
  );
  const app = await electron.launch({
    executablePath: require('electron'),
    args: ['.', '--gul-electron-test', `--user-data-dir=${profile}`],
    env: { ...environment, NODE_ENV: 'test', ...(ca ? { GUL_ELECTRON_TEST_CA: ca } : {}) },
  });
  try {
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    await privateLoginForm(
      page,
      (await readFile(join(directory, 'address'), 'utf8')).trim(),
      (await readFile(join(directory, 'join-password'), 'utf8')).trim(),
    );
    await page.getByLabel('Твой ник', { exact: true }).fill('source-picker-fixture');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    const show = page.getByRole('button', { name: 'Показать экран', exact: true });
    await show.click();
    const dialog = page.getByRole('dialog', { name: 'Демонстрация экрана', exact: true });
    await expect(dialog).toBeVisible({ timeout: 15_000 });
    await expect(dialog.getByRole('checkbox')).toHaveCount(0);
    const confirm = dialog.getByRole('button', { name: 'Показать', exact: true });
    await expect(confirm).toBeDisabled();
    await expect(dialog.locator('.capture-source-card').first()).toBeVisible();
    await expect
      .poll(() =>
        dialog
          .locator('.capture-source-preview img')
          .evaluateAll((images) =>
            images.some(
              (image) => image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0,
            ),
          ),
      )
      .toBe(true);
    await dialog.getByRole('button', { name: 'Отмена', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Остановить демонстрацию', exact: true })).toHaveCount(0);
    await expect(show).toBeEnabled();
    await show.click();
    await expect(dialog).toBeVisible();
    await dialog.getByRole('tab', { name: /Окна/u }).click();
    await expect(confirm).toBeDisabled();
    await dialog.getByRole('tab', { name: /Экраны/u }).click();
    await dialog.locator('.capture-source-card').first().click();
    await expect(dialog.getByRole('radio').first()).toBeChecked();
    await expect(confirm).toBeEnabled();
    await confirm.click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Остановить демонстрацию', exact: true })).toBeVisible({
      timeout: 25_000,
    });
    await expect
      .poll(
        () =>
          page
            .getByLabel('Предпросмотр своего экрана', { exact: true })
            .evaluate(
              (video: HTMLVideoElement) =>
                video.videoWidth > 0 && video.getVideoPlaybackQuality().totalVideoFrames > 3,
            ),
        { timeout: 15_000 },
      )
      .toBe(true);
    await page.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
    await expect(show).toBeEnabled();
    await show.click();
    await expect(dialog).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(show).toBeEnabled();
  } finally {
    await app.close();
    await rm(profile, { recursive: true, force: true });
  }
});
