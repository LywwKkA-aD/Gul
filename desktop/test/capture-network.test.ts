import assert from 'node:assert/strict';
import test from 'node:test';
import { installPermissions } from '../src/main/permissions.ts';

test('native audio networking is limited to the owned window and its exact current consent lease', () => {
  let request!: (
    details: { url: string; webContentsId?: number },
    callback: (result: { cancel: boolean }) => void,
  ) => void;
  let active = true;
  let destroyed = false;
  const url = `ws://127.0.0.1:8123/${'a'.repeat(48)}`;
  const window = {
    isDestroyed: () => destroyed,
    webContents: {
      id: 7,
      session: {
        setPermissionCheckHandler() {},
        setPermissionRequestHandler() {},
        setDevicePermissionHandler() {},
        on() {},
        webRequest: {
          onBeforeRequest: (handler: typeof request) => {
            request = handler;
          },
        },
      },
    },
  };
  installPermissions(
    window as never,
    { networkAllowed: (value: string) => value === 'gul://app/app.js' } as never,
    (value) => active && value === url,
  );
  const allowed = (value = url, id?: number) => {
    let result = true;
    request({ url: value, ...(id === undefined ? {} : { webContentsId: id }) }, ({ cancel }) => {
      result = !cancel;
    });
    return result;
  };
  assert.equal(allowed(url, 7), true);
  assert.equal(allowed(url, 8), false);
  assert.equal(allowed(url), false);
  assert.equal(allowed(url + '?extra=1', 7), false);
  assert.equal(allowed('gul://app/app.js', 7), true);
  active = false;
  assert.equal(allowed(url, 7), false);
  active = true;
  destroyed = true;
  assert.equal(allowed(url, 7), false);
});
