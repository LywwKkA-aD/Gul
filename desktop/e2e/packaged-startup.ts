import type * as Electron from 'electron';

type StartupEvent = Readonly<{
  kind: 'created' | 'closed' | 'loaded' | 'renderer-gone' | 'guard-detached';
  value?: string;
}>;
type StartupGlobal = typeof globalThis & {
  __gulPackagedStartupDiagnostics?: { events: readonly StartupEvent[] };
};

/** Serialized into the main process by the test; never installed by the packaged app. */
export function installPackagedStartupDiagnostics({ app, BrowserWindow }: typeof Electron): void {
  const state = { events: [] as readonly StartupEvent[] };
  (globalThis as StartupGlobal).__gulPackagedStartupDiagnostics = state;
  const record = (event: StartupEvent) => {
    state.events = [...state.events.slice(-15), Object.freeze(event)];
  };
  const observe = (window: Electron.BrowserWindow) => {
    record({ kind: 'created' });
    window.once('closed', () => record({ kind: 'closed' }));
    window.webContents.on('did-finish-load', () => {
      const url = window.webContents.getURL();
      record({
        kind: 'loaded',
        value: url === 'gul://app/index.html' ? 'app' : url === 'about:blank' ? 'blank' : 'other',
      });
    });
    window.webContents.on('render-process-gone', (_event, details) => {
      const reason = [
        'clean-exit',
        'abnormal-exit',
        'killed',
        'crashed',
        'oom',
        'launch-failed',
        'integrity-failure',
        'memory-eviction',
      ].includes(details.reason)
        ? details.reason
        : 'other';
      record({ kind: 'renderer-gone', value: reason });
    });
    window.webContents.debugger.on('detach', (_event, reason) => {
      record({ kind: 'guard-detached', value: reason === 'target closed' ? 'target-closed' : 'other' });
    });
  };
  for (const window of BrowserWindow.getAllWindows()) if (!window.isDestroyed()) observe(window);
  app.on('browser-window-created', (_event, window) => observe(window));
}

/** Readiness comes from Electron, avoiding Runtime.evaluate during the blank/app frame swap. */
export function readPackagedStartup({ BrowserWindow }: typeof Electron) {
  const windows = BrowserWindow.getAllWindows()
    .filter((window) => !window.isDestroyed())
    .map((window) => ({
      appLoaded: window.webContents.getURL() === 'gul://app/index.html',
      visible: window.isVisible(),
      loading: window.webContents.isLoadingMainFrame(),
      guardAttached: window.webContents.debugger.isAttached(),
    }));
  const own = windows[0];
  return {
    ready: windows.length === 1 && own.appLoaded && own.visible && !own.loading && own.guardAttached,
    windows,
    events: [...((globalThis as StartupGlobal).__gulPackagedStartupDiagnostics?.events ?? [])],
  };
}
