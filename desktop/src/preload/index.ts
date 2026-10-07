import { contextBridge, ipcRenderer } from 'electron';
import { publicError } from './errors.ts';
import { installDeviceAudioGuard } from './media-guard.ts';
import type {
  AudioState,
  BrokerState,
  ConnectInput,
  DesktopAPI,
  MediaGrant,
  MediaSession,
  ScreenRequest,
  ServerList,
  CaptureCapabilities,
  AppInfo,
  ScreenAudioLease,
} from '../shared/contracts.ts';

if (contextBridge.executeInMainWorld({ func: installDeviceAudioGuard }) !== true)
  throw new Error('GUL_MEDIA_GUARD_UNAVAILABLE');

async function invoke<T>(channel: string, value?: unknown): Promise<T> {
  try {
    return (await ipcRenderer.invoke(channel, value)) as T;
  } catch (error) {
    throw publicError(error);
  }
}

/** No generic IPC method, Electron event, broker bearer, or Node object enters the page. */
const api: DesktopAPI = Object.freeze({
  connect: (input: ConnectInput, rememberPassword = false) =>
    invoke<MediaSession>('gul:connect', { input, rememberPassword }),
  connectSaved: (address: string, username: string, rememberPassword = true) =>
    invoke<MediaSession>('gul:connect-saved', { address, username, rememberPassword }),
  servers: () => invoke<ServerList>('gul:servers'),
  forgetServer: (address: string) => invoke<void>('gul:forget-server', address),
  captureCapabilities: () => invoke<CaptureCapabilities>('gul:capture-capabilities'),
  screenAudioStart: () => invoke<ScreenAudioLease>('gul:screen-audio-start'),
  screenAudioStop: (leaseId: string) => invoke<void>('gul:screen-audio-stop', leaseId),
  onScreenAudioEnded: (listener: (leaseId: string) => void) => {
    if (typeof listener !== 'function') throw new TypeError('GUL_INPUT_INVALID');
    const handler = (_event: Electron.IpcRendererEvent, leaseId: unknown) => {
      if (typeof leaseId === 'string' && /^[a-f0-9]{32}$/u.test(leaseId)) listener(leaseId);
    };
    ipcRenderer.on('gul:screen-audio-ended', handler);
    return () => ipcRenderer.removeListener('gul:screen-audio-ended', handler);
  },
  appInfo: () => invoke<AppInfo>('gul:app-info'),
  openUpdate: () => invoke<void>('gul:open-update'),
  diagnostics: () => invoke<boolean>('gul:diagnostics'),
  recordDiagnostic: (event: string, metadata: unknown) =>
    invoke<void>('gul:record-diagnostic', { event, metadata }),
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
  setPushToTalk: (shortcut: string | null, mode: 'toggle' | 'hold' = 'toggle') =>
    invoke<void>('gul:set-push-to-talk', { shortcut, mode }),
  minimize: () => invoke<void>('gul:minimize'),
  maximize: () => invoke<void>('gul:maximize'),
  closeWindow: () => invoke<void>('gul:close-window'),
});
contextBridge.exposeInMainWorld('gul', api);
