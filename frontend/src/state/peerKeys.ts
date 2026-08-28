import type { ChannelNode } from './types';

/**
 * The prefix of a peer key that stands for one connection and nothing more.
 *
 * The rule and the prefixes are the Go side's (internal/mumble/peerkey.go),
 * which builds these keys and states the obligation in as many words: whoever
 * stores against an `s:` key has to drop it when the peer leaves. The engine
 * does exactly that in ForgetAbsentPeers. This side stored against the same
 * keys and never dropped anything.
 *
 * What that costs is not a leak. Murmur hands session ids out again, so the
 * volume and the local mute somebody set for a stranger become the volume and
 * the mute of the next stranger given that number - and only in this copy, so
 * the window shows a person silenced while the engine, which did sweep, plays
 * them. The user sees a muted row they can hear.
 *
 * It is reachable by anyone: a certificate is what earns the immortal `h:`
 * key, and the official Mumble client can connect without one.
 */
const MORTAL_PREFIX = 's:';

/** Whether a peer key dies with the connection that produced it. */
export function isMortalPeerKey(key: string): boolean {
  return key.startsWith(MORTAL_PREFIX);
}

/** Every peer key in the room, the whole tree walked. */
export function collectPeerKeys(node: ChannelNode): Set<string> {
  const keys = new Set<string>();
  const walk = (n: ChannelNode) => {
    for (const user of n.users) if (user.key !== '') keys.add(user.key);
    for (const child of n.children) walk(child);
  };
  walk(node);
  return keys;
}

/**
 * Drops the settings of peers who are no longer in the room, and only the ones
 * that were never meant to outlive the session.
 *
 * Returns the original object when nothing was dropped. That is not a
 * micro-optimisation: this runs on every tree update, several a second in a
 * busy room, and a fresh object each time would re-render every member row
 * that reads it.
 */
export function forgetAbsentPeers<T>(
  stored: Record<string, T>,
  present: Set<string>,
): Record<string, T> {
  const doomed = Object.keys(stored).filter((key) => isMortalPeerKey(key) && !present.has(key));
  if (doomed.length === 0) return stored;
  const kept: Record<string, T> = { ...stored };
  for (const key of doomed) delete kept[key];
  return kept;
}
