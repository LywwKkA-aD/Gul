// Who counts as speaking. Kept apart from the hook so the rule is testable
// without a store: state/speaking.ts is the thin wrapper over it.

/**
 * Whether a participant is speaking right now.
 *
 * Two sources, one indication. Remote peers arrive in `talking` from
 * user:talking; we never appear there, because our own voice does not come
 * back from the server - what we hear about ourselves is the transmit gate,
 * published as audio:selftalking and held in `selfTalking`.
 */
export function isSpeaking(
  isSelf: boolean,
  session: number,
  selfTalking: boolean,
  talking: ReadonlySet<number>,
): boolean {
  return isSelf ? selfTalking : talking.has(session);
}
