// Global push-to-talk as the UI sees it. The watch itself lives in Go
// (internal/hotkey, internal/core/hotkey.go); this module holds the status it
// reports and the one rule derived from it - which listener owns the key.

/** What this machine can do with the stored key. Mirrors core.HotkeyStatus:
    "hold" watches the key and sees its release, "toggle" only sees
    activations (one opens the microphone, the next closes it), "unsupported"
    cannot watch keys at all. */
export type HotkeyMode = 'hold' | 'toggle' | 'unsupported';

/** Runtime state of the global key, not a stored setting. */
export interface HotkeyStatus {
  mode: HotkeyMode;
  /** Russian, ready to render; empty when plain hold-to-talk needs no note. */
  reason: string;
  /** Russian, ready to render; empty when the last attempt to bind the stored
      key reported nothing. */
  error: string;
}

/** What the UI assumes when no readable status arrived: no global key, so the
    window-focused listener keeps the microphone reachable. */
export const DEFAULT_HOTKEY: HotkeyStatus = { mode: 'unsupported', reason: '', error: '' };

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/** Folds the hotkey part of a settings snapshot into the store's shape.
 *
 * A mode this build does not know reads as "unsupported" on purpose: that
 * keeps the window-focused listener, so the worst case is a key that only
 * works in focus - never a key that works nowhere. */
export function normalizeHotkey(raw: unknown): HotkeyStatus {
  if (typeof raw !== 'object' || raw === null) return DEFAULT_HOTKEY;
  const h = raw as Partial<Record<keyof HotkeyStatus, unknown>>;
  const mode = h.mode === 'hold' || h.mode === 'toggle' ? h.mode : 'unsupported';
  return { mode, reason: text(h.reason), error: text(h.error) };
}

/** True while the system-wide watch is actually running.
 *
 * The three conditions mirror core.applyGlobalPTT: the user asked for the
 * global key, this machine can watch keys, and the last attempt to bind the
 * stored key reported no error. A watch that failed leaves nothing driving
 * the gate, which is why the error counts. */
export function globalPTTRunning(globalPtt: boolean, hotkey: HotkeyStatus): boolean {
  return globalPtt && hotkey.mode !== 'unsupported' && hotkey.error === '';
}

/** True while the window-focused listener is the one that drives the gate.
 *
 * Exactly one source may drive it. Two would fight over the same key: in hold
 * mode they would double every press, and in toggle mode - where an
 * activation latches - the window listener would close what the global key
 * just opened. */
export function localPTTActive(globalPtt: boolean, hotkey: HotkeyStatus): boolean {
  return !globalPTTRunning(globalPtt, hotkey);
}
