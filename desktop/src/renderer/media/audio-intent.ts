import type { AudioState } from '../../shared/contracts.ts';

/** The broker reports effective mute while deafened; local manual mute intent
 * must remain available when the user turns playback back on.
 */
export function reconcileAudioIntent(requested: AudioState, confirmed: AudioState): AudioState {
  return Object.freeze({
    muted: confirmed.deafened ? requested.muted : confirmed.muted,
    deafened: confirmed.deafened,
  });
}
