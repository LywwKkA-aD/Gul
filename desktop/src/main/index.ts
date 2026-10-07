import { app, BrowserWindow, Menu, protocol, session } from 'electron';
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

protocol.registerSchemesAsPrivileged([
  {
    scheme: 'gul',
    privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true },
  },
]);
app.commandLine.appendSwitch('enable-features', 'GlobalShortcutsPortal');
const overrides = testOptions(process.env, app.isPackaged, process.argv);
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
  installDisplayCapture(ownWindow, authority);
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
    if (window === ownWindow) window = undefined;
    void authority.disconnect();
  });
  ownWindow.once('ready-to-show', () => {
    ownWindow.show();
  });
  await ownWindow.loadURL(`${APP_ORIGIN}/index.html`);
}

void app
  .whenReady()
  .then(async () => {
    Menu.setApplicationMenu(null);
    uninstallIPC = installIPC(authority, () => window);
    await createWindow();
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
  quitting = true;
  void authority.disconnect().finally(() => {
    uninstallIPC();
    app.quit();
  });
});
