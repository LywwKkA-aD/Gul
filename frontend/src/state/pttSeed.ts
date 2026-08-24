import type { ConnState } from './types';

// When the microphone indicator has to be read back from the engine.
//
// The voice engine is deliberately not restarted across a reconnect, so a key
// held through one keeps the gate open - but the store drops pttHeld on every
// non-connected state (state/store.ts) and core only emits audio:ptt on a
// change (internal/core/hotkey.go). Between the two, the indicator goes dark
// while the microphone is still live, and nothing would ever correct it: on
// the Wayland toggle path the key latches, so the next transition may be
// minutes away or never.

/**
 * True when the connection has just come back up, and with it the question of
 * what the transmit gate is doing.
 *
 * `null` is "nothing seen yet" - the first render, which has to ask too: the
 * window can be opened onto a session that is already connected.
 */
export function needsPttReseed(previous: ConnState | null, next: ConnState): boolean {
  return next === 'connected' && previous !== 'connected';
}
