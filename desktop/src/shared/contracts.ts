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
}
export interface BrokerState {
  readonly tree: ChannelNode;
  readonly selfSession: number;
  readonly selfChannel: number;
  readonly revision: number;
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
  readonly passwordStatus: 'saved' | 'missing' | 'unavailable' | 'locked' | 'save-failed';
}
export interface ServerList {
  readonly servers: readonly SavedServerInfo[];
  readonly storage: 'protected' | 'unavailable';
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
export interface ScreenAudioLease {
  readonly leaseId: string;
  readonly deviceLabel: string;
}
export interface AppInfo {
  readonly version: string;
  readonly update: { readonly version: string; readonly url: string } | null;
}
export interface DesktopAPI {
  connect(input: ConnectInput, rememberPassword?: boolean): Promise<MediaSession>;
  connectSaved(address: string, username: string, rememberPassword?: boolean): Promise<MediaSession>;
  servers(): Promise<ServerList>;
  forgetServer(address: string): Promise<void>;
  captureCapabilities(): Promise<CaptureCapabilities>;
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
