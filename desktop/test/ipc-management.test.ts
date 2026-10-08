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
  const directory = await mkdtemp(join(tmpdir(), 'gul-management-ipc-'));
  const key = `gul-management-ipc-${randomUUID()}`;
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
  const management: unknown[] = [];
  const window = { webContents: contents, isDestroyed: () => destroyed };
  const services = {
    members: {
      describe: (value: unknown) => {
        management.push(value);
        return { state: 'none', usable: false };
      },
      consent: (value: unknown) => {
        management.push(value);
        return { state: 'loaded', usable: true };
      },
      clear: async () => {},
    },
    importMemberCredential: async (_window: unknown, value: unknown) => {
      management.push(value);
      return { state: 'loaded', usable: true };
    },
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
    management,
    setDestroyed: () => {
      destroyed = true;
    },
    setConnected: () => {
      connected = true;
    },
  };
}

test('member-key metadata and native import use only the owned app frame, never arbitrary renderer paths', async (t) => {
  const f = await fixture(t);
  const names = [
    'gul:member-credential',
    'gul:import-member-credential',
    'gul:set-member-credential-consent',
    'gul:clear-member-credential',
  ];
  for (const name of names) {
    const invoke = f.handlers.get(name);
    assert.equal(typeof invoke, 'function');
    for (const event of [
      { sender: {}, senderFrame: f.frame },
      { sender: f.contents, senderFrame: { url: 'gul://app/index.html' } },
      { sender: f.contents, senderFrame: { url: 'https://other.invalid' } },
    ])
      await assert.rejects(async () => invoke!(event, { address: 'fixture' }), /GUL_IPC_DENIED/);
  }
  assert.deepEqual(f.management, []);
  const input = { address: 'livekit+vless://fixture.invalid', rememberIdentity: false };
  assert.deepEqual(await f.handlers.get('gul:import-member-credential')!(f.event, input), {
    state: 'loaded',
    usable: true,
  });
  assert.deepEqual(f.management, [input]);
  await assert.rejects(
    async () => f.handlers.get('gul:member-credential')!(f.event, { path: 'arbitrary' }),
    /GUL_INPUT_INVALID/,
  );
});
