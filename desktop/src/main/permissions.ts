import type { BrowserWindow } from 'electron';
import type { SessionAuthority } from './session.ts';
import { appPage, appOrigin, mediaPermission, displayMediaPreflight } from './security.ts';

export function installPermissions(window: BrowserWindow, authority: SessionAuthority): void {
  const session = window.webContents.session;
  const report = (
    stage: 'check' | 'request',
    permission: string,
    own: boolean,
    details: import('./security.ts').PermissionDetails,
    allowed: boolean,
  ) => {
    if (permission !== 'media' && permission !== 'display-capture') return;
    try {
      window.webContents.emit(
        'gul-permission-diagnostic',
        Object.freeze({
          stage,
          permission,
          own,
          mainFrame: details.isMainFrame === true,
          appFrame: appPage(details.requestingUrl),
          appOrigin: appOrigin(details.securityOrigin),
          mediaType: ['audio', 'video', 'unknown'].includes(details.mediaType ?? '')
            ? details.mediaType
            : 'absent',
          mediaCount: Array.isArray(details.mediaTypes) ? details.mediaTypes.length : -1,
          activeSession: authority.connected(),
          guardReady: window.webContents.debugger.isAttached(),
          allowed,
        }),
      );
    } catch {
      /* Diagnostics never affect the permission decision or a closing window. */
    }
  };
  session.setPermissionCheckHandler((contents, permission, origin, details) => {
    const own = contents === window.webContents && appOrigin(origin);
    if (permission === 'display-capture') {
      const allowed = own && details.isMainFrame && appPage(details.requestingUrl);
      report('check', permission, own, details, allowed);
      return allowed;
    }
    if (permission === 'loopback-network' || permission === 'fullscreen')
      return own && details.isMainFrame && appPage(details.requestingUrl) && authority.connected();
    const allowed = mediaPermission(permission, details, own);
    report('check', permission, own, details, allowed);
    return allowed;
  });
  session.setPermissionRequestHandler((contents, permission, callback, details) => {
    const own = contents === window.webContents;
    if (permission === 'display-capture') {
      const allowed = own && details.isMainFrame && appPage(details.requestingUrl);
      report('request', permission, own, details, allowed);
      callback(allowed);
      return;
    }
    if (permission === 'loopback-network' || permission === 'fullscreen') {
      callback(own && details.isMainFrame && appPage(details.requestingUrl) && authority.connected());
      return;
    }
    const allowed =
      mediaPermission(permission, details, own) ||
      displayMediaPreflight(
        permission,
        details,
        own,
        authority.connected(),
        window.webContents.debugger.isAttached(),
      );
    report('request', permission, own, details, allowed);
    callback(allowed);
  });
  session.setDevicePermissionHandler(() => false);
  session.on('will-download', (event) => {
    event.preventDefault();
  });
  session.webRequest.onBeforeRequest((details, callback) => {
    callback({ cancel: !authority.networkAllowed(details.url) });
  });
}
