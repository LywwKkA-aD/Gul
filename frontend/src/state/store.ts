import { create } from 'zustand';
import { SILENT_DB } from './types';
import type { ChannelNode, ChatMessage, ConnectionStatus, TofuPrompt } from './types';

interface GulState {
  status: ConnectionStatus;
  tree: ChannelNode | null;
  // Session chat history per channel id, appended from chat:message events.
  messages: Record<number, ChatMessage[]>;
  tofu: TofuPrompt | null;
  activeChannelId: number | null;

  // ── Voice (M2) ──────────────────────────────────────────────────────────
  /** Sessions currently speaking, from user:talking. Replaced, never mutated. */
  talkingSessions: ReadonlySet<number>;
  /** Latest meters from audio:levels, dBFS; SILENT_DB while the engine is idle. */
  micDb: number;
  outDb: number;
  /** Local mic/output gates. Mirror of what AudioService was last told. */
  muted: boolean;
  deafened: boolean;
  /** Per-user gain by certificate hash, 1.0 = unity. Survives reconnects. */
  userVolumes: Record<string, number>;
  /** Selected devices, "" = system default. */
  captureId: string;
  playbackId: string;
  settingsOpen: boolean;

  setStatus: (status: ConnectionStatus) => void;
  setTree: (tree: ChannelNode) => void;
  appendMessage: (message: ChatMessage) => void;
  setHistory: (channelId: number, messages: ChatMessage[]) => void;
  setTofu: (prompt: TofuPrompt | null) => void;
  setActiveChannel: (channelId: number | null) => void;
  setTalking: (session: number, talking: boolean) => void;
  setLevels: (micDb: number, outDb: number) => void;
  setVoiceGates: (muted: boolean, deafened: boolean) => void;
  setUserVolume: (hash: string, volume: number) => void;
  setDevices: (captureId: string, playbackId: string) => void;
  setSettingsOpen: (open: boolean) => void;
  reset: () => void;
}

const initialStatus: ConnectionStatus = { state: 'disconnected', server: '' };

/** Shared empty set: selectors must see a stable reference when nobody talks. */
const NO_TALKING: ReadonlySet<number> = new Set<number>();

export const useGulStore = create<GulState>((set) => ({
  status: initialStatus,
  tree: null,
  messages: {},
  tofu: null,
  activeChannelId: null,

  talkingSessions: NO_TALKING,
  micDb: SILENT_DB,
  outDb: SILENT_DB,
  muted: false,
  deafened: false,
  userVolumes: {},
  captureId: '',
  playbackId: '',
  settingsOpen: false,

  setStatus: (status) =>
    set((s) => {
      // Voice only runs while connected: anything else leaves stale halos and
      // a frozen meter behind, so drop both on every other transition.
      const live = status.state === 'connected';
      return {
        status,
        // A fresh disconnect invalidates the tree; history stays for the session.
        tree: status.state === 'disconnected' ? null : s.tree,
        activeChannelId:
          status.state === 'connected' && status.selfChannel !== undefined
            ? status.selfChannel
            : status.state === 'disconnected'
              ? null
              : s.activeChannelId,
        talkingSessions: live ? s.talkingSessions : NO_TALKING,
        micDb: live ? s.micDb : SILENT_DB,
        outDb: live ? s.outDb : SILENT_DB,
      };
    }),
  setTree: (tree) => set({ tree }),
  appendMessage: (message) =>
    set((s) => ({
      messages: {
        ...s.messages,
        [message.channelId]: [...(s.messages[message.channelId] ?? []), message],
      },
    })),
  setHistory: (channelId, messages) =>
    set((s) => ({ messages: { ...s.messages, [channelId]: messages } })),
  setTofu: (prompt) => set({ tofu: prompt }),
  setActiveChannel: (channelId) => set({ activeChannelId: channelId }),

  setTalking: (session, talking) =>
    set((s) => {
      // Returning the same state object is a real no-op in zustand, which
      // matters here: talking events arrive on every gate transition.
      if (s.talkingSessions.has(session) === talking) return s;
      const next = new Set(s.talkingSessions);
      if (talking) next.add(session);
      else next.delete(session);
      return { talkingSessions: next };
    }),
  setLevels: (micDb, outDb) => set({ micDb, outDb }),
  setVoiceGates: (muted, deafened) => set({ muted, deafened }),
  setUserVolume: (hash, volume) =>
    set((s) => ({ userVolumes: { ...s.userVolumes, [hash]: volume } })),
  setDevices: (captureId, playbackId) => set({ captureId, playbackId }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),

  reset: () =>
    set({
      status: initialStatus,
      tree: null,
      messages: {},
      tofu: null,
      activeChannelId: null,
      talkingSessions: NO_TALKING,
      micDb: SILENT_DB,
      outDb: SILENT_DB,
      settingsOpen: false,
    }),
}));

// Helpers

export function findChannel(root: ChannelNode | null, id: number): ChannelNode | null {
  if (!root) return null;
  if (root.id === id) return root;
  for (const child of root.children) {
    const hit = findChannel(child, id);
    if (hit) return hit;
  }
  return null;
}

export function selfUser(root: ChannelNode | null): { user: ChannelNode['users'][number]; channelId: number } | null {
  if (!root) return null;
  for (const u of root.users) if (u.isSelf) return { user: u, channelId: root.id };
  for (const child of root.children) {
    const hit = selfUser(child);
    if (hit) return hit;
  }
  return null;
}
