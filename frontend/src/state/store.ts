import { create } from 'zustand';
import { SILENT_DB } from './types.ts';
import type {
  ChannelNode,
  ChatMessage,
  ConnectionStatus,
  TofuPrompt,
  UpdateAvailable,
} from './types.ts';
import {
  PTT_KEY_DEFAULT,
  VAD_HANGOVER_DEFAULT,
  VAD_OPEN_DEFAULT,
  clampHangoverMs,
  clampOpenThreshold,
} from './gate.ts';
import { CUE_VOLUME_DEFAULT, clampCueVolume } from './settings.ts';
import { DEFAULT_HOTKEY } from './hotkey.ts';
import { collectPeerKeys, forgetAbsentPeers } from './peerKeys.ts';
import type { GateMode } from './gate.ts';
import type { GulSettings } from './settings.ts';
import type { HotkeyStatus } from './hotkey.ts';

interface GulState {
  status: ConnectionStatus;
  /** Smoothed TCP RTT of the active Mumble session; null until sampled. */
  pingMs: number | null;
  tree: ChannelNode | null;
  // Session chat history per channel id, appended from chat:message events.
  messages: Record<number, ChatMessage[]>;
  tofu: TofuPrompt | null;
  activeChannelId: number | null;
  /** A release newer than the one running, from the one check at startup.
      Null means there is nothing to say - which is also what every failure of
      that check looks like (internal/core/update.go). */
  update: UpdateAvailable | null;

  // ── Persisted settings (M4) ─────────────────────────────────────────────
  /** What the connect form starts on: the last connection the server
      accepted, from config.json. Never the password. */
  lastAddress: string;
  lastUsername: string;

  // ── Voice (M2) ──────────────────────────────────────────────────────────
  /** Sessions currently speaking, from user:talking. Replaced, never mutated. */
  talkingSessions: ReadonlySet<number>;
  /**
   * Our own microphone transmitting, from audio:selftalking. Our voice never
   * comes back from the server, so this is the only source of the local
   * speaking indication.
   */
  selfTalking: boolean;
  /** Latest meters from audio:levels, dBFS; SILENT_DB while the engine is idle. */
  micDb: number;
  outDb: number;
  /** Local mic/output gates. Mirror of what AudioService was last told. */
  muted: boolean;
  deafened: boolean;
  /** Per-user gain by certificate hash, 1.0 = unity. Survives reconnects. */
  userVolumes: Record<string, number>;
  /** Participants silenced on this machine, by the same certificate hash.
      Separate from the gain on purpose: unmuting gives back the gain the user
      chose, and the engine is what keeps it (internal/audio/users.go). */
  mutedUsers: Record<string, boolean>;
  /** Selected devices, "" = system default. */
  captureId: string;
  playbackId: string;
  /** Gain of the UI cues (join, leave, mute, unmute); 0 turns them off. */
  cueVolume: number;
  settingsOpen: boolean;

  // ── Transmit gate (M3) ──────────────────────────────────────────────────
  /** What opens the microphone. Mirror of what AudioService was last told. */
  gateMode: GateMode;
  /** VAD open threshold; the closing edge is derived from it (gate.ts). */
  vadOpen: number;
  /** VAD hangover tail in ms. */
  vadHangoverMs: number;
  /** KeyboardEvent.code of the push-to-talk key - a physical key, so it does
      not move when the keyboard layout changes. */
  pttKey: string;
  /** True while that key is held. Live session state, not a setting. */
  pttHeld: boolean;
  /** True while the settings field waits for the next key press. The PTT
      listener stands down meanwhile, so binding a key cannot transmit. */
  pttCapturing: boolean;
  /** Whether the user asked for the key to be watched system wide. */
  globalPtt: boolean;
  /** What this machine can do with that key, and what the last attempt to
      bind it reported. Runtime state from Go, not a stored setting. */
  hotkey: HotkeyStatus;

  setStatus: (status: ConnectionStatus) => void;
  setPingMs: (pingMs: number | null) => void;
  setTree: (tree: ChannelNode) => void;
  appendMessage: (message: ChatMessage) => void;
  setHistory: (channelId: number, messages: ChatMessage[]) => void;
  setTofu: (prompt: TofuPrompt | null) => void;
  setUpdate: (update: UpdateAvailable | null) => void;
  setActiveChannel: (channelId: number | null) => void;
  applySettings: (settings: GulSettings) => void;
  setRememberedConnection: (address: string, username: string) => void;
  setTalking: (session: number, talking: boolean) => void;
  setSelfTalking: (talking: boolean) => void;
  setLevels: (micDb: number, outDb: number) => void;
  setVoiceGates: (muted: boolean, deafened: boolean) => void;
  setUserVolume: (hash: string, volume: number) => void;
  setUserMuted: (hash: string, muted: boolean) => void;
  setDevices: (captureId: string, playbackId: string) => void;
  setSettingsOpen: (open: boolean) => void;
  setGateMode: (mode: GateMode) => void;
  setVadTuning: (open: number, hangoverMs: number) => void;
  setPttKey: (code: string) => void;
  setPttHeld: (held: boolean) => void;
  setPttCapturing: (capturing: boolean) => void;
  setGlobalPtt: (enabled: boolean) => void;
  setHotkey: (hotkey: HotkeyStatus) => void;
  setCueVolume: (volume: number) => void;
  reset: () => void;
}

const initialStatus: ConnectionStatus = { state: 'disconnected', server: '' };

/** Shared empty set: selectors must see a stable reference when nobody talks. */
const NO_TALKING: ReadonlySet<number> = new Set<number>();

export const useGulStore = create<GulState>((set) => ({
  status: initialStatus,
  pingMs: null,
  tree: null,
  messages: {},
  tofu: null,
  activeChannelId: null,
  update: null,

  lastAddress: '',
  lastUsername: '',

  talkingSessions: NO_TALKING,
  selfTalking: false,
  micDb: SILENT_DB,
  outDb: SILENT_DB,
  muted: false,
  deafened: false,
  userVolumes: {},
  mutedUsers: {},
  captureId: '',
  playbackId: '',
  cueVolume: CUE_VOLUME_DEFAULT,
  settingsOpen: false,

  gateMode: 'vad',
  vadOpen: VAD_OPEN_DEFAULT,
  vadHangoverMs: VAD_HANGOVER_DEFAULT,
  pttKey: PTT_KEY_DEFAULT,
  pttHeld: false,
  pttCapturing: false,
  globalPtt: false,
  hotkey: DEFAULT_HOTKEY,

  setStatus: (status) =>
    set((s) => {
      // Voice only runs while connected: anything else leaves stale halos and
      // a frozen meter behind, so drop both on every other transition.
      const live = status.state === 'connected';
      return {
        status,
        pingMs: live ? s.pingMs : null,
        // A fresh disconnect invalidates the tree; history stays for the session.
        tree: status.state === 'disconnected' ? null : s.tree,
        activeChannelId:
          status.state === 'connected' && status.selfChannel !== undefined
            ? status.selfChannel
            : status.state === 'disconnected'
              ? null
              : s.activeChannelId,
        talkingSessions: live ? s.talkingSessions : NO_TALKING,
        selfTalking: live ? s.selfTalking : false,
        micDb: live ? s.micDb : SILENT_DB,
        outDb: live ? s.outDb : SILENT_DB,
        // Nothing is on the wire while the session is down, so the indicator
        // goes with the halos and the meters. The listener keeps its own
        // record of what the engine was told and releases on the real keyup.
        pttHeld: live ? s.pttHeld : false,
      };
    }),
  setPingMs: (pingMs) => set((s) => (s.status.state === 'connected' ? { pingMs } : s)),
  // The roster is also what says who is still here, and two things in this
  // store are filed under keys that die with the peer they name. The engine
  // sweeps its own copy on the same event (internal/audio/engine.go,
  // ForgetAbsentPeers); until now this copy swept nothing, so a session id
  // Murmur handed out again arrived carrying the last owner's volume and
  // local mute - visible here, absent there, and the window would show a
  // person silenced while the engine played them.
  setTree: (tree) =>
    set((s) => {
      const present = collectPeerKeys(tree);
      const userVolumes = forgetAbsentPeers(s.userVolumes, present);
      const mutedUsers = forgetAbsentPeers(s.mutedUsers, present);
      return { tree, userVolumes, mutedUsers };
    }),
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
  setUpdate: (update) => set({ update }),
  setActiveChannel: (channelId) => set({ activeChannelId: channelId }),

  // Fetched once, before the first render: the connect form and the settings
  // modal read these on mount, and re-seeding them later would fight whatever
  // the user is typing.
  applySettings: (settings) =>
    set({
      lastAddress: settings.address,
      lastUsername: settings.username,
      captureId: settings.captureId,
      playbackId: settings.playbackId,
      gateMode: settings.gateMode,
      vadOpen: clampOpenThreshold(settings.vadOpen),
      vadHangoverMs: clampHangoverMs(settings.hangoverMs),
      pttKey: settings.pttKey,
      globalPtt: settings.globalPtt,
      hotkey: settings.hotkey,
      cueVolume: clampCueVolume(settings.cueVolume),
    }),

  // The Go side remembers the connect form once the server accepted it; this
  // is that value coming back, so a disconnect inside the same session finds
  // the form filled the way the next start would.
  setRememberedConnection: (address, username) =>
    set({ lastAddress: address, lastUsername: username }),

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
  setSelfTalking: (talking) => set((s) => (s.selfTalking === talking ? s : { selfTalking: talking })),
  setLevels: (micDb, outDb) => set({ micDb, outDb }),
  setVoiceGates: (muted, deafened) => set({ muted, deafened }),
  setUserVolume: (hash, volume) =>
    set((s) => ({ userVolumes: { ...s.userVolumes, [hash]: volume } })),
  setUserMuted: (hash, muted) =>
    set((s) => ({ mutedUsers: { ...s.mutedUsers, [hash]: muted } })),
  setDevices: (captureId, playbackId) => set({ captureId, playbackId }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),

  // Leaving push-to-talk cannot leave a key held: the mode that reads it is
  // gone, and the gate is reset on the engine side for the same reason.
  setGateMode: (mode) => set((s) => (s.gateMode === mode ? s : { gateMode: mode, pttHeld: false })),
  setVadTuning: (open, hangoverMs) =>
    set({ vadOpen: clampOpenThreshold(open), vadHangoverMs: clampHangoverMs(hangoverMs) }),
  setPttKey: (code) => set((s) => (s.pttKey === code ? s : { pttKey: code, pttHeld: false })),
  setPttHeld: (held) => set((s) => (s.pttHeld === held ? s : { pttHeld: held })),
  setPttCapturing: (capturing) => set({ pttCapturing: capturing }),

  // Turning the global key on or off changes who drives the gate, so a key
  // held right now is released with it (state/hotkey.ts, state/ptt.ts).
  setGlobalPtt: (enabled) =>
    set((s) => (s.globalPtt === enabled ? s : { globalPtt: enabled, pttHeld: false })),
  // Read back from the snapshot after every change that can re-point the
  // watch: whether the stored key could be bound is decided in Go.
  setHotkey: (hotkey) => set({ hotkey }),
  setCueVolume: (volume) => set({ cueVolume: clampCueVolume(volume) }),

  reset: () =>
    set({
      status: initialStatus,
      pingMs: null,
      tree: null,
      messages: {},
      tofu: null,
      activeChannelId: null,
      talkingSessions: NO_TALKING,
      selfTalking: false,
      micDb: SILENT_DB,
      outDb: SILENT_DB,
      settingsOpen: false,
      // Gate settings are settings and stay; what is live does not.
      pttHeld: false,
      pttCapturing: false,
      // muted and deafened are not session state: core keeps them across
      // connections and the tray shows them, so clearing them here would put
      // the window at odds with both (internal/core/selfaudio.go).
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
