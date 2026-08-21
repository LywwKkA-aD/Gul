// UI-side model types. Field names mirror the JSON tags of internal/domain
// (and the generated bindings in frontend/bindings/github.com/LywwKkA-aD/Gul/internal/domain/models.ts).
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

export interface ConnectionLatency {
  pingMs: number;
}

export interface TofuPrompt {
  server: string;
  oldFingerprint: string;
  newFingerprint: string;
}

/** Payload of user:talking - one gate transition of one session. */
export interface TalkingEvent {
  session: number;
  hash?: string;
  talking: boolean;
}

/** Payload of audio:levels - meters in dBFS, roughly every 50 ms. */
export interface AudioLevels {
  micDb: number;
  outDb: number;
}

/** One selectable device; id "" means the system default. */
export interface AudioDevice {
  id: string;
  name: string;
  isDefault: boolean;
}

/** Meter floor: the engine reports about -96 dBFS on digital silence, and
    that is also what we show while it is not running at all. */
export const SILENT_DB = -96;

/** Bottom of the visible meter range. Below -60 dBFS a mic is inaudible, so
    the whole usable span maps onto the bar instead of a stub at its left. */
export const METER_FLOOR_DB = -60;

/** dBFS -> 0..100 % of the meter width. */
export function meterPercent(db: number): number {
  if (!Number.isFinite(db)) return 0;
  const pct = ((db - METER_FLOOR_DB) / -METER_FLOOR_DB) * 100;
  return Math.min(100, Math.max(0, pct));
}

/** Per-user gain bounds for the member-list slider (1.0 = unity). */
export const VOLUME_MIN = 0;
export const VOLUME_MAX = 2;
export const VOLUME_STEP = 0.05;
export const VOLUME_UNITY = 1;

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
