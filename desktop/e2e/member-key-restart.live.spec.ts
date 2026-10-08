import { test, expect, _electron as electron, type ElectronApplication } from '@playwright/test';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { readPackagedStartup } from './packaged-startup.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_MANAGED_STAND_DIR;
test.skip(!fixture, 'Set GUL_MANAGED_STAND_DIR to an isolated managed REALITY fixture.');

test('explicit personal-key consent survives a full Electron restart using a real protected OS store', async () => {
  const directory = resolve(fixture!);
  const address = (await readFile(join(directory, 'address'), 'utf8')).trim();
  const password = (await readFile(join(directory, 'join-password'), 'utf8')).trim();
  const ownerPath = join(directory, 'owner-key.json');
  const ownerKey = JSON.parse(await readFile(ownerPath, 'utf8')) as { credential: string };
  const caFile = join(directory, 'ca.pem');
  await access(caFile);
  const profile = await mkdtemp(join(tmpdir(), 'gul-member-restart-'));
  let current: ElectronApplication | undefined;
  const launch = async () => {
    const app = await electron.launch({
      executablePath: require('electron'),
      args: [
        '.',
        '--gul-electron-test',
        ...(process.platform === 'linux' ? ['--password-store=gnome-libsecret'] : []),
        `--user-data-dir=${profile}`,
      ],
      env: { ...process.env, NODE_ENV: 'test', GUL_ELECTRON_TEST_CA: caFile },
    });
    current = app;
    await expect.poll(async () => (await app.evaluate(readPackagedStartup)).ready).toBe(true);
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    await expect
      .poll(() => page.evaluate(async () => (await window.gul.servers()).storage))
      .toBe('protected');
    return { app, page };
  };
  try {
    const first = await launch();
    // Only the native picker response is supplied by the fixture. Main performs
    // the production bounded file read, identity binding and encrypted save.
    await first.app.evaluate(({ dialog }, path) => {
      dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [path] });
    }, ownerPath);
    const staged = await first.page.evaluate(async (address) => {
      const value = await window.gul.importMemberCredential({ address, rememberIdentity: true });
      return { loaded: value?.state === 'loaded', saved: value?.state === 'saved', usable: value?.usable };
    }, address);
    expect(staged).toEqual({ loaded: true, saved: false, usable: true });
    const role = await first.page.evaluate(
      async ({ address, password }) => {
        const connected = await window.gul.connect(
          { address, password, username: 'saved-owner-fixture' },
          false,
        );
        return connected.member?.role;
      },
      { address, password },
    );
    expect(role).toBe('owner');
    const confirmed = await first.page.evaluate((address) => window.gul.memberCredential(address), address);
    expect(confirmed.state).toBe('saved');
    expect(confirmed.rememberIdentity).toBe(true);
    expect(Object.hasOwn(confirmed, 'credential')).toBe(false);
    const encrypted = await readFile(join(profile, 'members.json'), 'utf8');
    expect(encrypted.includes(ownerKey.credential)).toBe(false);
    await first.app.close();
    current = undefined;

    const second = await launch();
    const restored = await second.page.evaluate((address) => window.gul.memberCredential(address), address);
    expect(restored.state).toBe('saved');
    expect(restored.usable).toBe(true);
    expect(Object.hasOwn(restored, 'credential')).toBe(false);
    const restoredRole = await second.page.evaluate(
      async ({ address, password }) => {
        const connected = await window.gul.connect(
          { address, password, username: 'saved-owner-fixture' },
          false,
        );
        return connected.member?.role;
      },
      { address, password },
    );
    expect(restoredRole).toBe('owner');
  } finally {
    await current?.close();
    await rm(profile, { recursive: true, force: true });
  }
});
