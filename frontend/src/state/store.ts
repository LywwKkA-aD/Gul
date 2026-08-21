import { create } from 'zustand';
import type { ChannelNode, ChatMessage, ConnectionStatus, TofuPrompt } from './types';

interface GulState {
  status: ConnectionStatus;
  tree: ChannelNode | null;
  // Session chat history per channel id, appended from chat:message events.
  messages: Record<number, ChatMessage[]>;
  tofu: TofuPrompt | null;
  activeChannelId: number | null;

  setStatus: (status: ConnectionStatus) => void;
  setTree: (tree: ChannelNode) => void;
  appendMessage: (message: ChatMessage) => void;
  setHistory: (channelId: number, messages: ChatMessage[]) => void;
  setTofu: (prompt: TofuPrompt | null) => void;
  setActiveChannel: (channelId: number | null) => void;
  reset: () => void;
}

const initialStatus: ConnectionStatus = { state: 'disconnected', server: '' };

export const useGulStore = create<GulState>((set) => ({
  status: initialStatus,
  tree: null,
  messages: {},
  tofu: null,
  activeChannelId: null,

  setStatus: (status) =>
    set((s) => ({
      status,
      // A fresh disconnect invalidates the tree; history stays for the session.
      tree: status.state === 'disconnected' ? null : s.tree,
      activeChannelId:
        status.state === 'connected' && status.selfChannel !== undefined
          ? status.selfChannel
          : status.state === 'disconnected'
            ? null
            : s.activeChannelId,
    })),
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
  reset: () => set({ status: initialStatus, tree: null, messages: {}, tofu: null, activeChannelId: null }),
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
