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
export interface DesktopAPI {
  connect(input: ConnectInput): Promise<MediaSession>;
  disconnect(): Promise<void>;
  state(): Promise<BrokerState | null>;
  channel(id: number): Promise<MediaSession>;
  audio(state: AudioState): Promise<AudioState>;
  screen(request: ScreenRequest): Promise<MediaGrant>;
  onPushToTalk(listener: (pressed: boolean) => void): () => void;
  setPushToTalk(shortcut: string | null): Promise<void>;
  minimize(): Promise<void>;
  maximize(): Promise<void>;
  closeWindow(): Promise<void>;
}
export const APP_ORIGIN = 'gul://app';
