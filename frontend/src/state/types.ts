// Mirrors internal/domain/types.go. Keep field names in sync with the JSON
// tags there; these payloads arrive via Wails events.

export interface UserInfo {
  session: number;
  hash?: string;
  name: string;
  channelId: number;
  selfMute: boolean;
  selfDeaf: boolean;
  isSelf: boolean;
}

export interface ChannelNode {
  id: number;
  name: string;
  position: number;
  users: UserInfo[];
  children: ChannelNode[];
}

export interface ChatMessage {
  id: string;
  channelId: number;
  sender: string;
  senderHash?: string;
  html: string; // sanitized on the Go side: only b/i/u/a/br
  at: string;   // ISO timestamp
}

export type ConnState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting';

export interface ConnectionStatus {
  state: ConnState;
  server: string;
  error?: string;
  selfSession?: number;
  selfChannel?: number;
}

export interface TofuPrompt {
  server: string;
  oldFingerprint: string;
  newFingerprint: string;
}
