import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import test from 'node:test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  NativeHoldHotkey,
  parseHoldAccelerator,
  portalTrigger,
  type HotkeyProcess,
} from '../src/main/hotkeys.ts';

class FakeProcess extends EventEmitter {
  stdin = new PassThrough();
  stdout = new PassThrough();
  exitCode: number | null = null;
  killed = false;
  constructor() {
    super();
    this.stdin.on('finish', () => {
      this.exitCode = 0;
      this.emit('exit', 0, null);
    });
  }
  kill(): boolean {
    this.killed = true;
    this.exitCode = 1;
    this.emit('exit', 1, null);
    return true;
  }
}
function fixture() {
  const children: FakeProcess[] = [],
    states: boolean[] = [];
  let failures = 0;
  let launched: unknown[] = [];
  const hotkey = new NativeHoldHotkey({
    executable: '/trusted/gul-ptt.exe',
    platform: 'win32',
    parentPID: 42,
    emit: (state) => states.push(state),
    onFailure: () => {
      failures++;
    },
    spawn: (file, args, options) => {
      launched = [file, args, options];
      const child = new FakeProcess();
      children.push(child);
      return child as HotkeyProcess;
    },
  });
  return { hotkey, children, states, inspect: () => ({ failures, launched }) };
}

test('Windows hold bindings validate only supported physical keys and modifiers', () => {
  assert.deepEqual(parseHoldAccelerator('F8'), { virtualKey: 0x77, modifiers: 0 });
  assert.deepEqual(parseHoldAccelerator('Ctrl+Alt+Shift+Super+F24'), { virtualKey: 0x87, modifiers: 15 });
  assert.deepEqual(parseHoldAccelerator('CommandOrControl+Space'), { virtualKey: 0x20, modifiers: 1 });
  assert.deepEqual(parseHoldAccelerator('A'), { virtualKey: 0x41, modifiers: 0 });
  assert.deepEqual(parseHoldAccelerator('num9'), { virtualKey: 0x69, modifiers: 0 });
  for (const value of [
    '',
    'Ctrl',
    'Ctrl+Ctrl+F8',
    'F25',
    'Mouse1',
    'F8\nprivate',
    'Ctrl+Alt+Delete',
    'Super+L',
    'Ctrl+F8+F9',
    'F8; execute',
    '__proto__',
    'constructor+F8',
  ])
    assert.throws(() => parseHoldAccelerator(value), /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
});

test('missing native executable and startup timeout never advertise an available hold binding', async () => {
  const states: boolean[] = [];
  const missing = new NativeHoldHotkey({
    executable: join(tmpdir(), 'gul-no-such-native-helper.exe'),
    platform: 'win32',
    emit: (value) => states.push(value),
  });
  await assert.rejects(missing.register('F8'), /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
  assert.equal(missing.available(), false);
  assert.equal(states.at(-1), false);
  const child = new FakeProcess();
  const timeout = new NativeHoldHotkey({
    executable: '/trusted/gul-ptt.exe',
    platform: 'win32',
    startupTimeoutMs: 5,
    emit: (value) => states.push(value),
    spawn: () => child as HotkeyProcess,
  });
  await assert.rejects(timeout.register('F8'), /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
  assert.equal(timeout.available(), false);
  assert.equal(states.at(-1), false);
});

test('concurrent register cancels the stale startup, and dispose kills an unresponsive helper', async () => {
  const { hotkey, children, states } = fixture();
  const first = hotkey.register('F8');
  const firstResult = assert.rejects(first, /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
  await new Promise((resolve) => setImmediate(resolve));
  const second = hotkey.register('F9');
  await firstResult;
  await new Promise((resolve) => setImmediate(resolve));
  children[1].stdout.write('up\ndown\n');
  await second;
  children[1].stdin.removeAllListeners('finish');
  await hotkey.dispose();
  assert.equal(children[1].killed, true);
  assert.equal(states.at(-1), false);
});

test('helper startup uses validated numeric arguments, no shell, and remains muted until ready', async () => {
  const { hotkey, children, states, inspect } = fixture();
  const pending = hotkey.register('Ctrl+F8');
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(states, [false]);
  children[0].stdout.write('u');
  children[0].stdout.write('p\n');
  await pending;
  assert.equal(hotkey.available(), true);
  children[0].stdout.write('down\nup\n');
  assert.deepEqual(states, [false, true, false]);
  const [file, args, options] = inspect().launched as [string, string[], Record<string, unknown>];
  assert.equal(file, '/trusted/gul-ptt.exe');
  assert.deepEqual(args, ['119', '1', '42']);
  assert.equal(options.shell, false);
  assert.equal(options.windowsHide, true);
  assert.deepEqual(options.stdio, ['pipe', 'pipe', 'ignore']);
  await hotkey.dispose();
  assert.equal(hotkey.available(), false);
});

test('startup failure and unsupported systems expose a fixed error and fail closed', async () => {
  const { hotkey, children, states } = fixture();
  const pending = hotkey.register('F8');
  await new Promise((resolve) => setImmediate(resolve));
  children[0].emit('error', Error('private executable path or arguments'));
  await assert.rejects(pending, /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
  assert.equal(states.at(-1), false);
  const unsupported = new NativeHoldHotkey({
    executable: '/trusted/gul-ptt.exe',
    platform: 'darwin',
    emit: () => {},
    spawn: () => {
      throw Error('must not spawn');
    },
  });
  await assert.rejects(unsupported.register('F8'), /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
});

test('Linux portal trigger uses the XDG keysym grammar, never executable or shell text', () => {
  assert.equal(portalTrigger('Ctrl+Alt+Shift+Super+F8'), 'CTRL+ALT+SHIFT+LOGO+F8');
  assert.equal(portalTrigger('A'), 'a');
  assert.equal(portalTrigger('CommandOrControl+Space'), 'CTRL+space');
  assert.equal(portalTrigger('PageUp'), 'Prior');
  assert.equal(portalTrigger('num9'), 'KP_9');
  assert.equal(portalTrigger('Backspace'), 'BackSpace');
  assert.equal(portalTrigger('CapsLock'), 'Caps_Lock');
  for (const value of ['Mouse1', 'Ctrl+Ctrl+F8', 'private;execute', '__proto__'])
    assert.throws(() => portalTrigger(value), /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
});

test('Linux portal helper receives only the requested trigger, parent and allowlisted bus environment', async () => {
  const child = new FakeProcess();
  const states: boolean[] = [];
  let launched: unknown[] = [];
  const previousPassword = process.env.GUL_TEST_PASSWORD;
  const previousBus = process.env.DBUS_SESSION_BUS_ADDRESS;
  process.env.GUL_TEST_PASSWORD = 'never-pass-to-helper';
  process.env.DBUS_SESSION_BUS_ADDRESS = 'unix:path=/test/session-bus';
  const hotkey = new NativeHoldHotkey({
    executable: '/trusted/gul-ptt',
    platform: 'linux',
    parentPID: 42,
    emit: (state) => states.push(state),
    spawn: (file, args, options) => {
      launched = [file, args, options];
      return child as HotkeyProcess;
    },
  });
  try {
    const pending = hotkey.register('Ctrl+F8');
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(states, [false]);
    assert.equal(hotkey.available(), false);
    const [file, args, options] = launched as [string, string[], import('node:child_process').SpawnOptions];
    assert.equal(file, '/trusted/gul-ptt');
    assert.deepEqual(args, ['CTRL+F8', '42']);
    assert.equal(options.shell, false);
    assert.equal(options.env?.GUL_TEST_PASSWORD, undefined);
    assert.equal(options.env?.DBUS_SESSION_BUS_ADDRESS, 'unix:path=/test/session-bus');
    child.stdout.write('up\ndown\nup\n');
    await pending;
    assert.deepEqual(states, [false, true, false]);
    await hotkey.dispose();
  } finally {
    if (previousPassword === undefined) delete process.env.GUL_TEST_PASSWORD;
    else process.env.GUL_TEST_PASSWORD = previousPassword;
    if (previousBus === undefined) delete process.env.DBUS_SESSION_BUS_ADDRESS;
    else process.env.DBUS_SESSION_BUS_ADDRESS = previousBus;
  }
});

test('unexpected stdout, flooding or helper exit mutes immediately without leaking output', async () => {
  for (const output of ['private-token\n', 'x'.repeat(4097), 'down\n']) {
    const { hotkey, children, states } = fixture();
    const pending = hotkey.register('F8');
    await new Promise((resolve) => setImmediate(resolve));
    children[0].stdout.write(output);
    await assert.rejects(pending, /^Error: GUL_SHORTCUT_UNAVAILABLE$/u);
    assert.equal(states.at(-1), false);
  }
  const { hotkey, children, states, inspect } = fixture();
  const pending = hotkey.register('F8');
  await new Promise((resolve) => setImmediate(resolve));
  children[0].stdout.write('up\ndown\n');
  await pending;
  children[0].emit('exit', 1, null);
  assert.equal(states.at(-1), false);
  assert.equal(inspect().failures, 1);
  assert.equal(hotkey.available(), false);
});

test('replacement and disposal fence late events from the previous helper', async () => {
  const { hotkey, children, states } = fixture();
  let pending = hotkey.register('F8');
  await new Promise((resolve) => setImmediate(resolve));
  children[0].stdout.write('up\ndown\n');
  await pending;
  pending = hotkey.register('F9');
  await new Promise((resolve) => setImmediate(resolve));
  children[1].stdout.write('up\n');
  await pending;
  const length = states.length;
  children[0].stdout.write('down\n');
  assert.equal(states.length, length);
  await Promise.all([hotkey.dispose(), hotkey.dispose()]);
  children[1].stdout.write('down\n');
  assert.equal(states.at(-1), false);
});
