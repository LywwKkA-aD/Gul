// UI-side model types. Field names mirror the JSON tags of internal/domain
// (and the generated bindings in ../bindings/gul/internal/domain/models.ts).
// Nullability differs from the wire: events are normalized at the boundary
// (events.ts), so inside the app users/children are never null.

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

/** Wire shape of ChannelNode: Go nil slices arrive as null. */
export interface WireChannelNode {
  id: number;
  name: string;
  position: number;
  users: UserInfo[] | null;
  children: WireChannelNode[] | null;
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

export function normalizeTree(node: WireChannelNode): ChannelNode {
  return {
    id: node.id,
    name: node.name,
    position: node.position,
    users: node.users ?? [],
    children: (node.children ?? []).map(normalizeTree),
  };
}

/** Stable tint index (0..7) for a user: prefer the cert hash, fall back to name. */
export function tintFor(user: Pick<UserInfo, 'hash' | 'name'>): number {
  const key = user.hash || user.name;
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
  return h % 8;
}

export function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? '?';
  const second = parts.length > 1 ? parts[parts.length - 1][0] : (parts[0]?.[1] ?? '');
  return (first + second).toUpperCase();
}
