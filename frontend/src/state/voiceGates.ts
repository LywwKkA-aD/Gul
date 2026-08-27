import { useGulStore } from './store';
import type { UserInfo } from './types';
import { voiceGatesOf } from './voiceGatesRule';

/**
 * The microphone and monitor state to draw for one row (state/voiceGatesRule.ts).
 *
 * Each selector returns a boolean. One that built the pair as an object would
 * return a fresh reference every time, zustand compares with Object.is, and
 * every row would re-render on every store change. A row that is not ours
 * reads a constant, so it never subscribes to our gates at all.
 */
export function useVoiceGates(
  user: Pick<UserInfo, 'isSelf' | 'selfMute' | 'selfDeaf'>,
): { muted: boolean; deaf: boolean } {
  const selfMuted = useGulStore((s) => (user.isSelf ? s.muted : false));
  const selfDeafened = useGulStore((s) => (user.isSelf ? s.deafened : false));
  return voiceGatesOf(user.isSelf, user.selfMute, user.selfDeaf, selfMuted, selfDeafened);
}
