import { contextBridge, ipcRenderer } from 'electron';
import type {
  MemberCredentialInfo,
  ImportMemberCredential,
  RedeemInvitation,
  ManagementContext,
  MemberList,
  ChannelPermissions,
  ChannelCreate,
  ChannelUpdate,
  ChannelDelete,
  Invitation,
} from '../shared/management.ts';
import { publicError } from './errors.ts';
import { installDeviceAudioGuard } from './media-guard.ts';
import type { CapturePickerRequest } from '../shared/capture-picker.ts';
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
  PasswordStorageRecovery,
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
  unlockPasswordStorage: () => invoke<PasswordStorageRecovery>('gul:unlock-password-storage'),
  openPasswordStorage: () => invoke<boolean>('gul:open-password-storage'),
  forgetServer: (address: string) => invoke<void>('gul:forget-server', address),
  memberCredential: (address: string) => invoke<MemberCredentialInfo>('gul:member-credential', address),
  importMemberCredential: (input: ImportMemberCredential) =>
    invoke<MemberCredentialInfo | null>('gul:import-member-credential', input),
  setMemberCredentialConsent: (input: ImportMemberCredential) =>
    invoke<MemberCredentialInfo>('gul:set-member-credential-consent', input),
  clearMemberCredential: (address: string) => invoke<void>('gul:clear-member-credential', address),
  redeemInvitation: (input: RedeemInvitation) => invoke<MemberCredentialInfo>('gul:redeem-invitation', input),
  members: (context: ManagementContext) => invoke<MemberList>('gul:members', context),
  channelPermissions: (input: ManagementContext & { channelId: number }) =>
    invoke<ChannelPermissions>('gul:channel-permissions', input),
  createChannel: (input: ChannelCreate) => invoke<BrokerState>('gul:create-channel', input),
  updateChannel: (input: ChannelUpdate) => invoke<BrokerState>('gul:update-channel', input),
  deleteChannel: (input: ChannelDelete) => invoke<BrokerState>('gul:delete-channel', input),
  createInvitation: (context: ManagementContext) => invoke<Invitation>('gul:create-invitation', context),
  captureCapabilities: () => invoke<CaptureCapabilities>('gul:capture-capabilities'),
  onCapturePicker: (listener: (request: CapturePickerRequest | null) => void) => {
    if (typeof listener !== 'function') throw new TypeError('GUL_INPUT_INVALID');
    const handler = (_event: Electron.IpcRendererEvent, request: CapturePickerRequest | null) =>
      listener(request);
    ipcRenderer.on('gul:capture-picker', handler);
    return () => ipcRenderer.removeListener('gul:capture-picker', handler);
  },
  selectCaptureSource: (requestId: string, sourceKey: string | null) =>
    invoke<void>('gul:select-capture-source', { requestId, sourceKey }),
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
