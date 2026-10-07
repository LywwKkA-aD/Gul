import { globalShortcut, ipcMain, type BrowserWindow, type IpcMainInvokeEvent } from 'electron';
import type { SessionAuthority } from './session.ts';
import { appPage } from './security.ts';
import { audioInput, capturePickerInput, failure } from './validation.ts';
import type { AppServices } from './app-services.ts';
import { NativeHoldHotkey } from './hotkeys.ts';
import type { CaptureCapabilities, ScreenAudioLease } from '../shared/contracts.ts';
import { runWithCaptureReset } from './capture-reset.ts';

interface CaptureServices {
  capabilities(): Promise<CaptureCapabilities>;
  start(): Promise<ScreenAudioLease>;
  stop(leaseId: string): Promise<void>;
  reset(): Promise<void>;
  select(requestId: string, sourceKey: string | null): boolean;
}

/** Only this window's top-level app frame may invoke this fixed IPC allowlist. */
export function installIPC(
  authority: SessionAuthority,
  getWindow: () => BrowserWindow | undefined,
  services: AppServices,
  holdExecutable: string,
  capture: CaptureServices,
): () => void {
  let shortcut: string | undefined;
  let shortcutMode: 'toggle' | 'hold' = 'toggle';
  let registration = 0;
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
  const hold = new NativeHoldHotkey({
    executable: holdExecutable,
    emit: (value) => {
      if (!value || authority.connected()) emit(value);
    },
    onFailure: () => {
      emit(false);
      void authority.audio({ muted: true, deafened: false }).catch(() => {});
      services.journal.record('shortcut-failed', { code: 'GUL_SHORTCUT_UNAVAILABLE' });
    },
  });
  const handlers: Record<string, (event: IpcMainInvokeEvent, value?: unknown) => unknown> = {
    'gul:connect': async (_event, value) => {
      services.journal.record('connect-start');
      try {
        const result = await runWithCaptureReset(
          () => capture.reset(),
          () => services.connections.connect(value),
        );
        services.journal.record('connect-ok', { channelId: result.channelId });
        return result;
      } catch (error) {
        services.journal.record('connect-failed');
        throw error;
      }
    },
    'gul:connect-saved': (_event, value) =>
      runWithCaptureReset(
        () => capture.reset(),
        () => services.connections.connectSaved(value),
      ),
    'gul:servers': () => services.serverList(),
    'gul:forget-server': async (_event, value) => {
      if (typeof value !== 'string') throw failure('GUL_INPUT_INVALID');
      if (!(await services.servers.forget(value)).persisted) throw failure('GUL_STORAGE_WRITE_FAILED');
    },
    'gul:capture-capabilities': () => capture.capabilities(),
    'gul:select-capture-source': (_event, value) => {
      const { requestId, sourceKey } = capturePickerInput(value);
      if (!capture.select(requestId, sourceKey)) throw failure('GUL_INPUT_INVALID');
    },
    'gul:screen-audio-start': () => capture.start(),
    'gul:screen-audio-stop': (_event, value) => {
      if (typeof value !== 'string' || !/^[a-f0-9]{32}$/u.test(value)) throw failure('GUL_INPUT_INVALID');
      return capture.stop(value);
    },
    'gul:app-info': () => services.info(),
    'gul:open-update': () => services.openUpdate(),
    'gul:diagnostics': (event) => services.diagnostics(current(event)),
    'gul:record-diagnostic': (_event, value) => services.record(value),
    'gul:disconnect': async () => {
      emit(false);
      await runWithCaptureReset(
        () => capture.reset(),
        () => services.connections.disconnect(),
      );
    },
    'gul:state': () => authority.state(),
    'gul:channel': async (_event, value) => {
      emit(false);
      services.journal.record('channel-change', { channelId: value });
      return runWithCaptureReset(
        () => capture.reset(),
        () => authority.channel(value as number),
      );
    },
    'gul:audio': (_event, value) => {
      const state = audioInput(value);
      pressed = !state.muted && !state.deafened;
      return authority.audio(state);
    },
    'gul:screen': (_event, value) => authority.screen(value as never),
    'gul:set-push-to-talk': async (_event, value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw failure('GUL_INPUT_INVALID');
      const { shortcut: accelerator, mode } = value as Record<string, unknown>;
      if (
        !['toggle', 'hold'].includes(mode as string) ||
        (accelerator !== null &&
          (typeof accelerator !== 'string' ||
            !accelerator.trim() ||
            accelerator.length > 96 ||
            /[\u0000-\u001f]/u.test(accelerator)))
      )
        throw failure('GUL_INPUT_INVALID');
      const own = ++registration;
      emit(false);
      if (shortcut && shortcutMode === 'toggle') globalShortcut.unregister(shortcut);
      shortcut = undefined;
      await hold.dispose();
      if (own !== registration) throw failure('GUL_SHORTCUT_UNAVAILABLE');
      if (accelerator === null) return;
      shortcut = accelerator as string;
      shortcutMode = mode as 'toggle' | 'hold';
      try {
        if (shortcutMode === 'hold') await hold.register(shortcut);
        else if (
          !globalShortcut.register(shortcut, () => {
            if (authority.connected()) emit(!pressed);
          })
        )
          throw failure('GUL_SHORTCUT_UNAVAILABLE');
        emit(false);
      } catch {
        emit(false);
        if (own === registration) shortcut = undefined;
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
    ++registration;
    emit(false);
    if (shortcut && shortcutMode === 'toggle') globalShortcut.unregister(shortcut);
    void hold.dispose();
    for (const name of Object.keys(handlers)) ipcMain.removeHandler(name);
  };
}
