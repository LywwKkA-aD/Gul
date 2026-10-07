import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { installPackagedStartupDiagnostics, readPackagedStartup } from '../e2e/packaged-startup.ts';

class WindowFixture extends EventEmitter {
  destroyed = false;
  visible = true;
  url = 'gul://app/index.html';
  loading = false;
  attached = true;
  debugger = new EventEmitter();
  webContents = Object.assign(new EventEmitter(), {
    getURL: () => this.url,
    isLoadingMainFrame: () => this.loading,
    debugger: Object.assign(this.debugger, { isAttached: () => this.attached }),
  });
  isDestroyed() {
    return this.destroyed;
  }
  isVisible() {
    return this.visible;
  }
}

function fixture(windows = [new WindowFixture()]) {
  const app = new EventEmitter();
  const api = { app, BrowserWindow: { getAllWindows: () => windows } };
  return { windows, app, api: api as never };
}

test('packaged readiness requires the exact final app URL, visible loaded window and attached guard', () => {
  const { api, windows } = fixture();
  assert.equal(readPackagedStartup(api).ready, true);
  const window = windows[0];
  for (const [key, value] of [
    ['url', 'about:blank'],
    ['url', 'gul://app/index.html?other'],
    ['loading', true],
    ['visible', false],
    ['attached', false],
    ['destroyed', true],
  ] as const) {
    const original = window[key];
    Object.assign(window, { [key]: value });
    assert.equal(readPackagedStartup(api).ready, false);
    Object.assign(window, { [key]: original });
  }
  assert.equal(readPackagedStartup(fixture([]).api).ready, false);
  assert.equal(readPackagedStartup(fixture([new WindowFixture(), new WindowFixture()]).api).ready, false);
});

test('startup snapshot only contains scalar readiness and bounded allowlisted lifecycle events', () => {
  const { api, windows, app } = fixture();
  installPackagedStartupDiagnostics(api);
  const window = windows[0];
  window.webContents.emit('did-finish-load');
  window.webContents.emit('render-process-gone', undefined, { reason: 'crashed', exitCode: 99999 });
  window.debugger.emit('detach', undefined, 'unexpected-private-reason');
  window.url = 'https://private.example/path?token=unexpected-private-reason';
  window.webContents.emit('did-finish-load');
  window.webContents.emit('render-process-gone', undefined, { reason: 'unexpected-private-reason' });
  window.emit('closed');
  const created = new WindowFixture();
  app.emit('browser-window-created', undefined, created);
  created.debugger.emit('detach', undefined, 'target closed');
  const snapshot = readPackagedStartup(api);
  assert.equal(JSON.stringify(snapshot).includes('unexpected-private-reason'), false);
  assert.equal(JSON.stringify(snapshot).includes('private.example'), false);
  assert.equal(JSON.stringify(snapshot).includes('99999'), false);
  assert.deepEqual(snapshot.events.slice(-8), [
    { kind: 'loaded', value: 'app' },
    { kind: 'renderer-gone', value: 'crashed' },
    { kind: 'guard-detached', value: 'other' },
    { kind: 'loaded', value: 'other' },
    { kind: 'renderer-gone', value: 'other' },
    { kind: 'closed' },
    { kind: 'created' },
    { kind: 'guard-detached', value: 'target-closed' },
  ]);
  for (let index = 0; index < 40; index++) created.webContents.emit('did-finish-load');
  assert.equal(readPackagedStartup(api).events.length, 16);
});

test('serialized main-side observers are self-contained and do not depend on renderer execution', () => {
  const { api, windows } = fixture();
  const install = new Function(`return (${installPackagedStartupDiagnostics.toString()})`)();
  const inspect = new Function(`return (${readPackagedStartup.toString()})`)();
  install(api);
  windows[0].visible = false;
  assert.equal(inspect(api).ready, false);
  windows[0].visible = true;
  assert.equal(inspect(api).ready, true);
});
