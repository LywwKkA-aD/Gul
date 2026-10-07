import { globalShortcut, ipcMain, type BrowserWindow, type IpcMainInvokeEvent } from 'electron';
import type { SessionAuthority } from './session.ts';
import { appPage } from './security.ts';
import { audioInput, failure } from './validation.ts';

/** Only this window's top-level app frame may invoke this fixed IPC allowlist. */
export function installIPC(
  authority: SessionAuthority,
  getWindow: () => BrowserWindow | undefined,
): () => void {
  let shortcut: string | undefined;
  let pressed = false;
  const current = (event: IpcMainInvokeEvent): BrowserWindow => {
    const window = getWindow();
    if (
      !window ||
      window.isDestroyed() ||
      event.sender !== window.webContents ||
      event.senderFrame !== window.webContents.mainFrame ||
      !appPage(event.senderFrame?.url)
    )
      throw failure('GUL_IPC_DENIED');
    return window;
  };
  const emit = (value: boolean) => {
    pressed = value;
    const window = getWindow();
    if (shortcut && window && !window.isDestroyed()) window.webContents.send('gul:push-to-talk', value);
  };
  const handlers: Record<string, (event: IpcMainInvokeEvent, value?: unknown) => unknown> = {
    'gul:connect': (_event, value) => authority.connect(value as never),
    'gul:disconnect': async () => {
      emit(false);
      await authority.disconnect();
    },
    'gul:state': () => authority.state(),
    'gul:channel': (_event, value) => {
      emit(false);
      return authority.channel(value as number);
    },
    'gul:audio': (_event, value) => {
      const state = audioInput(value);
      pressed = !state.muted && !state.deafened;
      return authority.audio(state);
    },
    'gul:screen': (_event, value) => authority.screen(value as never),
    'gul:set-push-to-talk': (_event, value) => {
      if (
        value !== null &&
        (typeof value !== 'string' || !value.trim() || value.length > 96 || /[\u0000-\u001f]/u.test(value))
      )
        throw failure('GUL_INPUT_INVALID');
      if (shortcut) globalShortcut.unregister(shortcut);
      shortcut = undefined;
      emit(false);
      if (value === null) return;
      try {
        // Electron supplies no global key-up callback. The global shortcut is
        // an explicit press-again toggle; focused hold belongs to the page.
        if (
          !globalShortcut.register(value as string, () => {
            if (authority.connected()) emit(!pressed);
          })
        )
          throw failure('GUL_SHORTCUT_UNAVAILABLE');
        shortcut = value as string;
        emit(false);
      } catch {
        throw failure('GUL_SHORTCUT_UNAVAILABLE');
      }
    },
    'gul:minimize': (event) => {
      current(event).minimize();
    },
    'gul:maximize': (event) => {
      const window = current(event);
      if (window.isMaximized()) window.unmaximize();
      else window.maximize();
    },
    'gul:close-window': (event) => {
      current(event).close();
    },
  };
  for (const [name, handler] of Object.entries(handlers))
    ipcMain.handle(name, (event, value) => {
      current(event);
      return handler(event, value);
    });
  return () => {
    if (shortcut) globalShortcut.unregister(shortcut);
    for (const name of Object.keys(handlers)) ipcMain.removeHandler(name);
  };
}
