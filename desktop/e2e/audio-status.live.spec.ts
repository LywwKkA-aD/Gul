import { test, expect, _electron as electron, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(!fixture, 'Start the isolated REALITY fixture.');
const name = 'audio-status-fixture';

async function audioStatus(page: Page, muted: boolean, deafened: boolean) {
  const micOff = muted || deafened;
  const rows = [page.getByRole('navigation', { name: 'Голосовые каналы' }), page.locator('.members')];
  for (const container of rows) {
    const row = container.getByRole('button', { name: `Ваши настройки: ${name}`, exact: true });
    await expect(row).toBeVisible();
    await expect(row.getByRole('img', { name: 'Микрофон выключен', exact: true })).toHaveCount(
      micOff ? 1 : 0,
    );
    await expect(row.getByRole('img', { name: 'Звук выключен', exact: true })).toHaveCount(deafened ? 1 : 0);
    await expect(row.locator('.icon-slash')).toHaveCount(Number(micOff) + Number(deafened));
    if (micOff)
      await expect(
        row.getByRole('img', { name: 'Микрофон выключен', exact: true }).locator('.icon-slash'),
      ).toBeVisible();
    if (deafened)
      await expect(
        row.getByRole('img', { name: 'Звук выключен', exact: true }).locator('.icon-slash'),
      ).toBeVisible();
  }
  const mic = page.getByRole('button', {
    name: micOff ? 'Включить микрофон' : 'Выключить микрофон',
    exact: true,
  });
  const sound = page.getByRole('button', {
    name: deafened ? 'Включить звук' : 'Выключить звук',
    exact: true,
  });
  await expect(mic.locator('.icon-slash')).toHaveCount(micOff ? 1 : 0);
  await expect(sound.locator('.icon-slash')).toHaveCount(deafened ? 1 : 0);
}
async function logoLoaded(page: Page) {
  const logo = page.getByRole('img', { name: 'Gul', exact: true });
  await expect(logo).toBeVisible();
  await expect
    .poll(() =>
      logo.evaluate(
        (image: HTMLImageElement) =>
          image.complete && image.naturalWidth === 1024 && image.naturalHeight === 1024,
      ),
    )
    .toBe(true);
}

test('own audio badges match footer immediately and across channel changes; official logos load', async () => {
  const directory = resolve(fixture!);
  const profile = await mkdtemp(join(tmpdir(), 'gul-audio-status-'));
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
    await logoLoaded(page);
    await privateLoginForm(
      page,
      (await readFile(join(directory, 'address'), 'utf8')).trim(),
      (await readFile(join(directory, 'join-password'), 'utf8')).trim(),
    );
    await page.getByLabel('Твой ник', { exact: true }).fill(name);
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    await logoLoaded(page);
    await audioStatus(page, false, false);
    await page.getByRole('button', { name: 'Выключить микрофон', exact: true }).click();
    await audioStatus(page, true, false);
    await page.getByRole('button', { name: 'Выключить звук', exact: true }).click();
    await audioStatus(page, true, true);
    for (const channel of ['Игра', 'Общая']) {
      await page.getByRole('button', { name: channel, exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible();
      await audioStatus(page, true, true);
    }
    await page.screenshot({ path: 'test-results/audio-status-muted.png' });
    await page.getByRole('button', { name: 'Включить звук', exact: true }).click();
    await audioStatus(page, true, false);
    await page.getByRole('button', { name: 'Включить микрофон', exact: true }).click();
    await audioStatus(page, false, false);
  } finally {
    await app.close();
    await rm(profile, { recursive: true, force: true });
  }
});
