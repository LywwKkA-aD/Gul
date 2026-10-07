import assert from 'node:assert/strict';
import test from 'node:test';
import { TrayLifecycle, type TrayHandle, type TrayWindow } from '../src/main/tray.ts';

function fixture(platform = 'win32', usable = true) {
  const counts = {
    hidden: 0,
    shown: 0,
    focused: 0,
    restored: 0,
    destroyed: 0,
    cleanup: 0,
    quit: 0,
    prevented: 0,
  };
  let actions!: { show: () => void; quit: () => void };
  const tray: TrayHandle = {
    isDestroyed: () => false,
    getBounds: () => ({ width: usable ? 16 : 0, height: 16 }),
    destroy: () => {
      counts.destroyed++;
    },
  };
  const window: TrayWindow = {
    isDestroyed: () => false,
    isMinimized: () => true,
    hide: () => {
      counts.hidden++;
    },
    show: () => {
      counts.shown++;
    },
    focus: () => {
      counts.focused++;
    },
    restore: () => {
      counts.restored++;
    },
  };
  const lifecycle = new TrayLifecycle({
    platform,
    window: () => window,
    createTray: (callbacks) => {
      actions = callbacks;
      return tray;
    },
    cleanup: async () => {
      counts.cleanup++;
    },
    quit: () => {
      counts.quit++;
    },
  });
  const event = {
    preventDefault: () => {
      counts.prevented++;
    },
  };
  return { lifecycle, counts, tray, window, event, actions: () => actions };
}

test('Windows and macOS close hide only while a working tray can reopen the window', () => {
  for (const platform of ['win32', 'darwin']) {
    const { lifecycle, counts, event, actions } = fixture(platform);
    assert.equal(lifecycle.initialize(), true);
    assert.equal(lifecycle.initialize(), true);
    assert.equal(lifecycle.handleClose(event), 'hide');
    assert.equal(counts.hidden, 1);
    assert.equal(counts.cleanup, 0);
    actions().show();
    assert.equal(counts.restored, 1);
    assert.equal(counts.shown, 1);
    assert.equal(counts.focused, 1);
  }
});

test('Linux close exits; an unavailable tray never strands Windows behind a hidden window', async () => {
  for (const [platform, usable] of [
    ['linux', true],
    ['win32', false],
  ] as const) {
    const { lifecycle, counts, event } = fixture(platform, usable);
    lifecycle.initialize();
    assert.equal(lifecycle.handleClose(event), 'quit');
    await lifecycle.requestQuit();
    assert.equal(counts.hidden, 0);
    assert.equal(counts.cleanup, 1);
    assert.equal(counts.quit, 1);
  }
});

test('explicit quit awaits media cleanup and destroys tray exactly once', async () => {
  const { lifecycle, counts, actions, event } = fixture();
  lifecycle.initialize();
  actions().quit();
  assert.equal(lifecycle.handleClose(event), 'close');
  await Promise.all([lifecycle.requestQuit(), lifecycle.requestQuit()]);
  assert.equal(counts.cleanup, 1);
  assert.equal(counts.destroyed, 1);
  assert.equal(counts.quit, 1);
  assert.equal(counts.hidden, 0);
});

test('destroyed tray or closed window falls back to quit and its restore action is harmless', async () => {
  const { lifecycle, counts, tray, window, event, actions } = fixture();
  lifecycle.initialize();
  tray.isDestroyed = () => true;
  window.isDestroyed = () => true;
  actions().show();
  assert.equal(counts.shown, 0);
  assert.equal(lifecycle.handleClose(event), 'quit');
  await lifecycle.requestQuit();
  assert.equal(counts.quit, 1);
});

test('native tray errors never prevent exit; cleanup failure still ends the process safely', async () => {
  let quit = 0;
  const lifecycle = new TrayLifecycle({
    platform: 'win32',
    window: () => undefined,
    createTray: () => {
      throw Error('native tray unavailable');
    },
    cleanup: async () => {
      throw Error('private cleanup exception');
    },
    quit: () => {
      quit++;
    },
  });
  assert.equal(lifecycle.initialize(), false);
  assert.equal(lifecycle.handleClose({ preventDefault() {} }), 'quit');
  await lifecycle.requestQuit();
  assert.equal(quit, 1);
});

test('disposal is idempotent, and a tray without valid bounds is not usable', () => {
  const { lifecycle, counts, tray } = fixture();
  tray.getBounds = () => {
    throw Error('native bounds unavailable');
  };
  assert.equal(lifecycle.initialize(), false);
  lifecycle.dispose();
  lifecycle.dispose();
  assert.equal(counts.destroyed, 1);
});
