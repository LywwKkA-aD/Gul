import type { ConnState } from './types';

/** Which screen a connection state belongs on.
 *
 * The main screen may only replace the connect form once a session has
 * existed. 'reconnecting' dims it and puts the reconnect banner on top, which
 * is a lie for an attempt that never connected - and it unmounts the form,
 * the only place status.error is rendered. An attempt that is merely waiting
 * (a rate-limited or full relay retries on its own) stays 'connecting' and
 * keeps the form, its message and its cancel action on screen. */
export function showsMainScreen(state: ConnState): boolean {
  return state === 'connected' || state === 'reconnecting';
}
