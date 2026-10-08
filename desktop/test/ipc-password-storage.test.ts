import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { randomUUID } from 'node:crypto';
import { build } from 'esbuild';

type Invoke = (event: unknown, value?: unknown) => Promise<unknown> | unknown;

async function fixture(t: test.TestContext) {
  const directory = await mkdtemp(join(tmpdir(), 'gul-password-ipc-'));
  const key = `gul-password-ipc-${randomUUID()}`;
  const handlers = new Map<string, Invoke>();
  const scope = {
    ipcMain: {
      handle: (name: string, handler: Invoke) => handlers.set(name, handler),
      removeHandler: (name: string) => handlers.delete(name),
    },
    globalShortcut: {},
  };
  Reflect.set(globalThis, key, scope);
  t.after(async () => {
    Reflect.deleteProperty(globalThis, key);
    await rm(directory, { recursive: true, force: true });
  });
  const output = join(directory, 'ipc.mjs');
  await build({
    entryPoints: [fileURLToPath(new URL('../src/main/ipc.ts', import.meta.url))],
    bundle: true,
    platform: 'node',
    format: 'esm',
    outfile: output,
    plugins: [
      {
        name: 'isolated-electron',
        setup(builder) {
          builder.onResolve({ filter: /^electron$/ }, () => ({ path: 'electron', namespace: 'fixture' }));
          builder.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({
            contents: `const state=globalThis[${JSON.stringify(key)}]; export const ipcMain=state.ipcMain,globalShortcut=state.globalShortcut;`,
          }));
        },
      },
    ],
  });
  const { installIPC } = await import(pathToFileURL(output).href);
  const frame = { url: 'gul://app/index.html' };
  const contents = { mainFrame: frame, send() {} };
  let destroyed = false;
  let connected = false;
  let unlocks = 0;
  let opens = 0;
  const window = { webContents: contents, isDestroyed: () => destroyed };
  const services = {
    journal: { record() {} },
    passwordStorage: {
      unlock: async () => {
        unlocks++;
        return { state: 'unlocked', restartRequired: true };
      },
      open: async () => {
        opens++;
        return true;
      },
    },
  };
  const cleanup = installIPC({ connected: () => connected }, () => window, services, '/unused', {});
  t.after(cleanup);
  const event = { sender: contents, senderFrame: frame };
  return {
    handlers,
    event,
    contents,
    frame,
    counts: () => ({ unlocks, opens }),
    setDestroyed: () => {
      destroyed = true;
    },
    setConnected: () => {
      connected = true;
    },
  };
}

test('password recovery actions only expose bounded results from the owned top-level app frame', async (t) => {
  const setup = await fixture(t);
  assert.equal(typeof setup.handlers.get('gul:unlock-password-storage'), 'function');
  assert.deepEqual(await setup.handlers.get('gul:unlock-password-storage')!(setup.event), {
    state: 'unlocked',
    restartRequired: true,
  });
  assert.equal(await setup.handlers.get('gul:open-password-storage')!(setup.event), true);
  assert.deepEqual(setup.counts(), { unlocks: 1, opens: 1 });
});

test('foreign frames, senders and destroyed windows cannot start a system keyring prompt', async (t) => {
  const setup = await fixture(t);
  for (const name of ['gul:unlock-password-storage', 'gul:open-password-storage']) {
    const invoke = setup.handlers.get(name);
    assert.equal(typeof invoke, 'function');
    for (const event of [
      { sender: {}, senderFrame: setup.frame },
      { sender: setup.contents, senderFrame: { url: 'gul://app/index.html' } },
      { sender: setup.contents, senderFrame: { url: 'https://other.invalid/' } },
    ])
      await assert.rejects(async () => invoke!(event), /GUL_IPC_DENIED/u);
  }
  setup.setDestroyed();
  await assert.rejects(
    async () => setup.handlers.get('gul:unlock-password-storage')!(setup.event),
    /GUL_IPC_DENIED/u,
  );
  assert.deepEqual(setup.counts(), { unlocks: 0, opens: 0 });
});

test('password recovery never accepts a renderer command, path, password or an active media session', async (t) => {
  const setup = await fixture(t);
  for (const name of ['gul:unlock-password-storage', 'gul:open-password-storage']) {
    const invoke = setup.handlers.get(name);
    assert.equal(typeof invoke, 'function');
    for (const value of [null, '/bin/sh', { command: 'arbitrary' }, { password: 'test-only' }])
      await assert.rejects(async () => invoke!(setup.event, value), /GUL_INPUT_INVALID/u);
  }
  setup.setConnected();
  for (const name of ['gul:unlock-password-storage', 'gul:open-password-storage'])
    await assert.rejects(async () => setup.handlers.get(name)!(setup.event), /GUL_INPUT_INVALID/u);
  assert.deepEqual(setup.counts(), { unlocks: 0, opens: 0 });
});
