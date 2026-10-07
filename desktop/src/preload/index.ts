import { contextBridge, ipcRenderer } from 'electron';
import { publicError } from './errors.ts';
import type {
  AudioState,
  BrokerState,
  ConnectInput,
  DesktopAPI,
  MediaGrant,
  MediaSession,
  ScreenRequest,
} from '../shared/contracts.ts';

async function invoke<T>(channel: string, value?: unknown): Promise<T> {
  try {
    return (await ipcRenderer.invoke(channel, value)) as T;
  } catch (error) {
    throw publicError(error);
  }
}

/** No generic IPC method, Electron event, broker bearer, or Node object enters the page. */
const api: DesktopAPI = Object.freeze({
  connect: (input: ConnectInput) => invoke<MediaSession>('gul:connect', input),
  disconnect: () => invoke<void>('gul:disconnect'),
  state: () => invoke<BrokerState | null>('gul:state'),
  channel: (id: number) => invoke<MediaSession>('gul:channel', id),
  audio: (state: AudioState) => invoke<AudioState>('gul:audio', state),
  screen: (request: ScreenRequest) => invoke<MediaGrant>('gul:screen', request),
  onPushToTalk: (listener: (pressed: boolean) => void) => {
    if (typeof listener !== 'function') throw new TypeError('GUL_INPUT_INVALID');
    const handler = (_event: Electron.IpcRendererEvent, pressed: unknown) => {
      if (typeof pressed === 'boolean') listener(pressed);
    };
    ipcRenderer.on('gul:push-to-talk', handler);
    return () => {
      ipcRenderer.removeListener('gul:push-to-talk', handler);
    };
  },
  setPushToTalk: (shortcut: string | null) => invoke<void>('gul:set-push-to-talk', shortcut),
  minimize: () => invoke<void>('gul:minimize'),
  maximize: () => invoke<void>('gul:maximize'),
  closeWindow: () => invoke<void>('gul:close-window'),
});
contextBridge.exposeInMainWorld('gul', api);
