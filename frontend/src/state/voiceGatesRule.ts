// Which microphone and monitor state a row shows. Kept apart from the hook so
// the rule is testable without a store: state/voiceGates.ts is the thin
// wrapper over it.

/**
 * The gates to draw for one participant.
 *
 * Two sources, one indication, and for our own row they disagree for a moment
 * on every click. Remote peers come from the channel tree, which is the only
 * thing that knows about them. Our own flags are in that tree as well, but the
 * server puts them there: it learns of a change only when our packet arrives,
 * and the tree it sends in the meantime still carries the previous pair. Any
 * event from anybody else - a join, a leave, somebody else's mute - produces
 * such a tree.
 *
 * So our own row is drawn from what core holds instead. That is the state the
 * click actually set, the same one the bottom bar and the tray already show,
 * and it means the icon on our name and the button that changed it can never
 * be seen contradicting each other. It cost nothing to be wrong here for a
 * round trip, except that this is the one row the user is watching when they
 * press the button.
 */
export function voiceGatesOf(
  isSelf: boolean,
  rowMuted: boolean,
  rowDeaf: boolean,
  selfMuted: boolean,
  selfDeafened: boolean,
): { muted: boolean; deaf: boolean } {
  return isSelf ? { muted: selfMuted, deaf: selfDeafened } : { muted: rowMuted, deaf: rowDeaf };
}
