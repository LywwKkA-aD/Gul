import type { BrowserWindow } from 'electron';
import type { SessionAuthority } from './session.ts';
import { appPage, mediaPermission } from './security.ts';

export function installPermissions(window: BrowserWindow, authority: SessionAuthority): void {
  const session = window.webContents.session;
  session.setPermissionCheckHandler((contents, permission, origin, details) => {
    const own = contents === window.webContents && origin === 'gul://app';
    if (permission === 'display-capture') return own && details.isMainFrame && appPage(details.requestingUrl);
    if (permission === 'loopback-network' || permission === 'fullscreen')
      return own && details.isMainFrame && appPage(details.requestingUrl) && authority.connected();
    return mediaPermission(permission, details, own);
  });
  session.setPermissionRequestHandler((contents, permission, callback, details) => {
    const own = contents === window.webContents;
    if (permission === 'display-capture') {
      callback(own && details.isMainFrame && appPage(details.requestingUrl));
      return;
    }
    if (permission === 'loopback-network' || permission === 'fullscreen') {
      callback(own && details.isMainFrame && appPage(details.requestingUrl) && authority.connected());
      return;
    }
    callback(mediaPermission(permission, details, own));
  });
  session.setDevicePermissionHandler(() => false);
  session.on('will-download', (event) => {
    event.preventDefault();
  });
  session.webRequest.onBeforeRequest((details, callback) => {
    callback({ cancel: !authority.networkAllowed(details.url) });
  });
}
