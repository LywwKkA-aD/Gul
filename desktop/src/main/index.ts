import { app, BrowserWindow, Menu, protocol, session, Tray, nativeImage } from 'electron';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createRealityGateway } from '../transport/gateway.ts';
import { APP_ORIGIN } from '../shared/contracts.ts';
import { SessionAuthority } from './session.ts';
import { appPage, testOptions } from './security.ts';
import { installAppProtocol } from './protocol.ts';
import { installPermissions } from './permissions.ts';
import { installDisplayCapture } from './capture.ts';
import { installIPC } from './ipc.ts';
import { AppServices } from './app-services.ts';
import { TrayLifecycle } from './tray.ts';
import { installMediaGuard } from './media-guard.ts';
import { NativeScreenAudio } from './screen-audio.ts';
import type { DisplayCaptureConsent } from './capture-consent.ts';
import { failure } from './validation.ts';
import { claimApplicationInstance } from './application-instance.ts';

protocol.registerSchemesAsPrivileged([
  {
    scheme: 'gul',
    privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true },
  },
]);
app.commandLine.appendSwitch('enable-features', 'GlobalShortcutsPortal');
const overrides = testOptions(process.env, app.isPackaged, process.argv);
const primaryInstance = claimApplicationInstance(app, process.env, app.isPackaged, process.argv);
if (!primaryInstance) app.exit(0);
const resourceRoot = app.isPackaged ? process.resourcesPath : join(app.getAppPath(), 'resources');
const xrayPath =
  overrides.xrayPath ??
  join(
    resourceRoot,
    'xray',
    `${process.platform}-${process.arch}`,
    process.platform === 'win32' ? 'xray.exe' : 'xray',
  );
const authority = new SessionAuthority(async ({ address, password }) =>
  createRealityGateway({
    address,
    password,
    origin: APP_ORIGIN,
    xrayPath,
    ...(overrides.caFile ? { ca: await readFile(overrides.caFile) } : {}),
  }),
);
let window: BrowserWindow | undefined;
let quitting = false;
let uninstallIPC = () => {};
let services: AppServices;
let screenAudio: NativeScreenAudio | undefined;
let displayConsent: DisplayCaptureConsent | undefined;
async function closeCapture(): Promise<void> {
  displayConsent?.invalidate();
  await screenAudio?.close();
}
async function captureCapabilities() {
  return services.capabilities({ linuxExcludedAudio: (await screenAudio?.available()) ?? false });
}
const lifecycle = new TrayLifecycle({
  platform: process.platform,
  window: () => window,
  createTray: ({ show, quit }) => {
    if (process.platform === 'linux') return undefined;
    const iconPath = app.isPackaged
      ? join(process.resourcesPath, 'appicon.png')
      : join(app.getAppPath(), '..', 'build', 'appicon.png');
    const icon = nativeImage.createFromPath(iconPath).resize({ width: 22, height: 22 });
    if (icon.isEmpty()) return undefined;
    const tray = new Tray(icon);
    tray.setToolTip('Gul');
    tray.setContextMenu(
      Menu.buildFromTemplate([
        { label: 'Открыть Gul', click: show },
        { type: 'separator' },
        { label: 'Выйти', click: quit },
      ]),
    );
    tray.on('click', show);
    tray.on('double-click', show);
    return tray;
  },
  cleanup: async () => {
    await closeCapture();
    await services?.close();
    uninstallIPC();
  },
  quit: () => {
    quitting = true;
    app.quit();
  },
});

async function createWindow(): Promise<void> {
  const ownSession = session.fromPartition('persist:gul-desktop', { cache: false });
  if (!(await ownSession.protocol.isProtocolHandled('gul')))
    installAppProtocol(ownSession, join(app.getAppPath(), 'dist', 'renderer'));
  window = new BrowserWindow({
    title: 'Gul',
    width: 1180,
    height: 780,
    minWidth: 850,
    minHeight: 560,
    backgroundColor: '#131720',
    show: false,
    frame: false,
    webPreferences: {
      session: ownSession,
      preload: join(app.getAppPath(), 'dist', 'preload', 'index.cjs'),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webSecurity: true,
      allowRunningInsecureContent: false,
      webviewTag: false,
      spellcheck: false,
      autoplayPolicy: 'no-user-gesture-required',
    },
  });
  const ownWindow = window;
  installPermissions(ownWindow, authority);
  const testCapture =
    !app.isPackaged &&
    process.env.NODE_ENV === 'test' &&
    process.argv.includes('--gul-electron-test') &&
    process.env.GUL_ELECTRON_TEST_CAPTURE_APPROVED === '1';
  const ownConsent = installDisplayCapture(ownWindow, authority, {
    getCapabilities: captureCapabilities,
    ...(testCapture
      ? {
          pick: async (sources) => ({
            response: sources.findIndex((source) => source.id.startsWith('screen:')) + 1,
            checkboxChecked: true,
          }),
        }
      : {}),
  });
  const ownAudio = new NativeScreenAudio({
    executable: join(resourceRoot, 'audio-capture', `${process.platform}-${process.arch}`, 'gul-audio'),
    consent: ownConsent,
    onEnded: (leaseId) => {
      if (!ownWindow.isDestroyed()) ownWindow.webContents.send('gul:screen-audio-ended', leaseId);
    },
  });
  displayConsent = ownConsent;
  screenAudio = ownAudio;
  const closeOwnCapture = async () => {
    ownConsent.invalidate();
    await ownAudio.close();
  };
  ownWindow.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  ownWindow.webContents.on('will-navigate', (event, url) => {
    if (!appPage(url)) event.preventDefault();
  });
  ownWindow.webContents.on('will-frame-navigate', (event) => {
    if (!event.isMainFrame || !appPage(event.url)) event.preventDefault();
  });
  ownWindow.webContents.on('will-attach-webview', (event) => {
    event.preventDefault();
  });
  ownWindow.on('closed', () => {
    void closeOwnCapture();
    if (window === ownWindow) window = undefined;
    void authority.disconnect();
  });
  ownWindow.on('close', (event) => {
    lifecycle.handleClose(event);
  });
  // Initialize the renderer target before attaching the private debugger. This blank
  // page cannot use Gul IPC or acquire media; the app is loaded only after registration.
  await ownWindow.loadURL('about:blank');
  await installMediaGuard(ownWindow.webContents.debugger, () => {
    void closeOwnCapture();
    void authority.disconnect();
    // A detached guard must also release any capture that was already running.
    setImmediate(() => {
      if (!ownWindow.isDestroyed()) ownWindow.destroy();
    });
  });
  await ownWindow.loadURL(`${APP_ORIGIN}/index.html`);
  if (!ownWindow.isDestroyed()) ownWindow.show();
}

if (primaryInstance) {
  app.on('second-instance', () => {
    if (!window || window.isDestroyed()) return;
    if (window.isMinimized()) window.restore();
    window.show();
    window.focus();
  });
  void app
    .whenReady()
    .then(async () => {
      Menu.setApplicationMenu(null);
      services = new AppServices(authority);
      await services.initialize();
      uninstallIPC = installIPC(
        authority,
        () => window,
        services,
        join(
          resourceRoot,
          'ptt',
          `${process.platform}-${process.arch}`,
          process.platform === 'win32' ? 'gul-ptt.exe' : 'gul-ptt',
        ),
        {
          capabilities: captureCapabilities,
          start: () => {
            if (!screenAudio) throw failure('GUL_SCREEN_AUDIO_UNAVAILABLE');
            return screenAudio.start();
          },
          stop: (leaseId) => screenAudio?.stop(leaseId) ?? Promise.resolve(),
          reset: closeCapture,
        },
      );
      await createWindow();
      if (!overrides.caFile && !process.argv.includes('--gul-electron-test')) lifecycle.initialize();
      app.on('activate', () => {
        if (!window) void createWindow();
      });
    })
    .catch(() => {
      app.exit(1);
    });
  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
  app.on('before-quit', (event) => {
    if (quitting) return;
    event.preventDefault();
    void lifecycle.requestQuit();
  });
}
