import type { CapturePickerRequest } from './capture-picker.ts';
import type {
  MemberIdentity,
  ManagementContext,
  ChannelCreate,
  ChannelUpdate,
  ChannelDelete,
  ChannelPermissions,
  MemberList,
  Invitation,
  MemberCredentialInfo,
  ImportMemberCredential,
  RedeemInvitation,
} from './management.ts';

/** Main owns broker credentials; only short-lived media grants cross IPC. */
export interface AudioState {
  readonly muted: boolean;
  readonly deafened: boolean;
}
export interface ConnectInput {
  readonly address: string;
  readonly username: string;
  readonly password: string;
}
export interface MediaGrant {
  readonly url: string;
  readonly token: string;
  readonly identity: string;
  readonly room: string;
  readonly ownerIdentity: string;
  readonly sessionId: number;
  readonly channelId: number;
  readonly revision: number;
}
export interface MediaSession {
  readonly epoch: number;
  readonly sessionId: number;
  readonly identity: string;
  readonly name: string;
  readonly channelId: number;
  readonly revision: number;
  readonly grant: MediaGrant;
  readonly serverId?: string | null;
  readonly member?: MemberIdentity;
  readonly catalogVersion?: number;
}
export interface UserInfo {
  readonly session: number;
  readonly key: string;
  readonly name: string;
  readonly channelId: number;
  readonly selfMute: boolean;
  readonly selfDeaf: boolean;
  readonly isSelf: boolean;
}
export interface ChannelNode {
  readonly id: number;
  readonly name: string;
  readonly position: number;
  readonly users: readonly UserInfo[] | null;
  readonly children: readonly ChannelNode[] | null;
  readonly version?: number;
  readonly access?: 'open' | 'restricted';
  readonly canJoin?: boolean;
}
export interface BrokerState {
  readonly tree: ChannelNode;
  readonly selfSession: number;
  readonly selfChannel: number;
  readonly revision: number;
  readonly serverId?: string | null;
  readonly member?: MemberIdentity;
  readonly catalogVersion?: number;
}
export interface ScreenRequest {
  readonly channelId: number;
  readonly revision: number;
}
export interface SavedServerInfo {
  readonly address: string;
  readonly username: string;
  readonly lastUsed: number;
  readonly hasPassword: boolean;
  readonly rememberPassword: boolean;
  readonly passwordStatus: 'saved' | 'missing' | 'unavailable' | 'locked' | 'unreadable' | 'save-failed';
}
export interface PasswordStorageInfo {
  readonly provider: 'gnome' | 'other' | 'unavailable';
  readonly state: 'ready' | 'locked' | 'missing' | 'unavailable';
  readonly restartRequired: boolean;
}
export interface PasswordStorageRecovery {
  readonly state: 'unlocked' | 'cancelled' | 'missing' | 'unavailable';
  readonly restartRequired: boolean;
}
export interface ServerList {
  readonly servers: readonly SavedServerInfo[];
  readonly storage: 'protected' | 'unavailable';
  readonly passwordStorage?: PasswordStorageInfo;
  readonly lastSave: {
    readonly address: string;
    readonly status: 'saved' | 'not-requested' | 'unavailable' | 'encrypt-failed' | 'write-failed';
    readonly persisted: boolean;
  } | null;
}
export interface CaptureCapabilities {
  readonly platform: string;
  readonly backend: 'wasapi' | 'pipewire-pulse' | 'coreaudio' | 'none';
  readonly systemAudio: boolean;
  readonly ownAudioExcluded: boolean;
  readonly audioServer: 'detected' | 'not-detected' | 'not-required';
  readonly details: string;
  readonly picker: 'portal' | 'application';
}
export interface LinuxScreenAudioLease {
  readonly leaseId: string;
  readonly deviceLabel: string;
  readonly url?: never;
}
export interface WindowsScreenAudioLease {
  readonly leaseId: string;
  readonly url: string;
  readonly deviceLabel?: never;
}
export type ScreenAudioLease = LinuxScreenAudioLease | WindowsScreenAudioLease;
export interface AppInfo {
  readonly version: string;
  readonly update: { readonly version: string; readonly url: string } | null;
}
export interface DesktopAPI {
  connect(input: ConnectInput, rememberPassword?: boolean): Promise<MediaSession>;
  connectSaved(address: string, username: string, rememberPassword?: boolean): Promise<MediaSession>;
  servers(): Promise<ServerList>;
  forgetServer(address: string): Promise<void>;
  unlockPasswordStorage(): Promise<PasswordStorageRecovery>;
  openPasswordStorage(): Promise<boolean>;
  memberCredential(address: string): Promise<MemberCredentialInfo>;
  importMemberCredential(input: ImportMemberCredential): Promise<MemberCredentialInfo | null>;
  setMemberCredentialConsent(input: ImportMemberCredential): Promise<MemberCredentialInfo>;
  clearMemberCredential(address: string): Promise<void>;
  redeemInvitation(input: RedeemInvitation): Promise<MemberCredentialInfo>;
  members(context: ManagementContext): Promise<MemberList>;
  channelPermissions(input: ManagementContext & { readonly channelId: number }): Promise<ChannelPermissions>;
  createChannel(input: ChannelCreate): Promise<BrokerState>;
  updateChannel(input: ChannelUpdate): Promise<BrokerState>;
  deleteChannel(input: ChannelDelete): Promise<BrokerState>;
  createInvitation(context: ManagementContext): Promise<Invitation>;
  captureCapabilities(): Promise<CaptureCapabilities>;
  onCapturePicker(listener: (request: CapturePickerRequest | null) => void): () => void;
  selectCaptureSource(requestId: string, sourceKey: string | null): Promise<void>;
  screenAudioStart(): Promise<ScreenAudioLease>;
  screenAudioStop(leaseId: string): Promise<void>;
  onScreenAudioEnded(listener: (leaseId: string) => void): () => void;
  appInfo(): Promise<AppInfo>;
  openUpdate(): Promise<void>;
  diagnostics(): Promise<boolean>;
  recordDiagnostic(event: string, metadata: unknown): Promise<void>;
  disconnect(): Promise<void>;
  state(): Promise<BrokerState | null>;
  channel(id: number): Promise<MediaSession>;
  audio(state: AudioState): Promise<AudioState>;
  screen(request: ScreenRequest): Promise<MediaGrant>;
  onPushToTalk(listener: (pressed: boolean) => void): () => void;
  setPushToTalk(shortcut: string | null, mode?: 'toggle' | 'hold'): Promise<void>;
  minimize(): Promise<void>;
  maximize(): Promise<void>;
  closeWindow(): Promise<void>;
}
export const APP_ORIGIN = 'gul://app';
