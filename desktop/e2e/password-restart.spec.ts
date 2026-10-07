import { test, expect, _electron as electron, type ElectronApplication } from '@playwright/test';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';
import { readPackagedStartup } from './packaged-startup.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(!fixture, 'Set GUL_ELECTRON_STAND_DIR to the isolated REALITY fixture.');

test('explicit remember consent and encrypted login survive a full Electron process restart', async () => {
  const directory = resolve(fixture!);
  const address = (await readFile(join(directory, 'address'), 'utf8')).trim();
  const password = (await readFile(join(directory, 'join-password'), 'utf8')).trim();
  const caFile = join(directory, 'ca.pem');
  const ca = await access(caFile)
    .then(() => caFile)
    .catch(() => undefined);
  const profile = await mkdtemp(join(tmpdir(), 'gul-password-restart-'));
  let current: ElectronApplication | undefined;
  const launch = async () => {
    const app = await electron.launch({
      executablePath: require('electron'),
      args: [
        '.',
        '--gul-electron-test',
        '--use-fake-device-for-media-stream',
        ...(process.platform === 'linux' ? ['--password-store=gnome-libsecret'] : []),
        `--user-data-dir=${profile}`,
      ],
      env: { ...process.env, NODE_ENV: 'test', ...(ca ? { GUL_ELECTRON_TEST_CA: ca } : {}) },
    });
    current = app;
    await expect
      .poll(async () => (await app.evaluate(readPackagedStartup)).ready, { timeout: 15_000 })
      .toBe(true);
    return { app, page: await app.firstWindow() };
  };
  try {
    const first = await launch();
    await expect(first.page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    const remember = first.page.getByRole('checkbox', {
      name: 'Запомнить пароль на этом компьютере',
      exact: true,
    });
    // A real protected OS provider is required; the test never substitutes a fake keyring.
    await expect(remember).toBeEnabled();
    await privateLoginForm(first.page, address, password);
    await first.page.getByLabel('Твой ник', { exact: true }).fill('saved-restart-fixture');
    await remember.check();
    await first.page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(first.page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    await expect(first.page.getByRole('button', { name: 'Отключиться', exact: true })).toBeVisible();
    const stored = await first.page.evaluate(async () => {
      const list = await window.gul.servers();
      return {
        protected: list.storage === 'protected',
        saved: list.servers[0]?.hasPassword,
        remember: list.servers[0]?.rememberPassword,
        status: list.lastSave?.status,
        persisted: list.lastSave?.persisted,
      };
    });
    expect(stored).toEqual({
      protected: true,
      saved: true,
      remember: true,
      status: 'saved',
      persisted: true,
    });
    await first.app.close();
    current = undefined;

    const reopened = await launch();
    await expect
      .poll(() =>
        reopened.page.evaluate(
          (value) =>
            document.querySelector<HTMLInputElement>('input[aria-label="Адрес сервера"]')?.value === value,
          address,
        ),
      )
      .toBe(true);
    await expect(reopened.page.getByLabel('Твой ник', { exact: true })).toHaveValue('saved-restart-fixture');
    const passwordInput = reopened.page.getByLabel('Пароль', { exact: true });
    await expect(passwordInput).toHaveValue('');
    await expect(passwordInput).toHaveAttribute('placeholder', 'Пароль сохранён — ввод не нужен');
    await expect(
      reopened.page.getByRole('checkbox', { name: 'Запомнить пароль на этом компьютере', exact: true }),
    ).toBeChecked();
    // The renderer receives no saved secret; the blank form resolves it exclusively in main.
    await reopened.page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(reopened.page.getByText('Голос подключён', { exact: true })).toBeVisible({
      timeout: 25_000,
    });
  } finally {
    await current?.close();
    await rm(profile, { recursive: true, force: true });
  }
});
