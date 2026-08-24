import { isSpeaking } from './speakingRule';
import { useGulStore } from './store';
import type { UserInfo } from './types';

/**
 * Whether a participant is speaking right now (state/speakingRule.ts).
 *
 * The selector returns a boolean on purpose. Returning the Set (or anything
 * derived from it) would re-render every row on every gate transition, and an
 * unstable reference would spin useSyncExternalStore forever.
 */
export function useSpeaking(user: Pick<UserInfo, 'session' | 'isSelf'>): boolean {
  return useGulStore((s) => isSpeaking(user.isSelf, user.session, s.selfTalking, s.talkingSessions));
}
