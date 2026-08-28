import type { UserInfo } from '../state/types';

/**
 * Whether this client offers volume and local mute for a member row.
 *
 * Extracted from the component because it was wrong there and nothing could
 * see it. The rule used to be `!isSelf && hash !== ''`, and the hash comes from
 * the server: it is the fingerprint of the certificate the client presented.
 * Once the tunnel contract took the client's own TLS session away, every
 * session became anonymous, every hash came back empty, and the whole block -
 * the slider and the mute button both - stopped rendering for everybody.
 *
 * The key replaces the hash and is never empty for a real peer
 * (internal/mumble/peerkey.go), so the only row without controls is our own:
 * our stream never comes back to us, and there is nothing to turn down.
 */
export function canAdjust(user: Pick<UserInfo, 'isSelf' | 'key'>): boolean {
  return !user.isSelf && user.key !== '';
}
