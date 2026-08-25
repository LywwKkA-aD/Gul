import { useState, type CSSProperties } from 'react';
import { cx } from './cx';

/** The eight muted tints assigned to users by index.
 *  The ONLY hard-coded hex values allowed outside tokens.css - copied verbatim
 *  from the prototype (docs/design/prototype-source.html, line 10132:
 *  `tints = [...]`). Keep in sync with that array, nothing else. */
export const AVATAR_TINTS = [
  '#3E5C9A',
  '#7B5AA6',
  '#2A7A87',
  '#A9723C',
  '#A0495A',
  '#4A7A4E',
  '#5A5FA0',
  '#86577F',
] as const;

/** Sizes actually used by the prototype: 20 in the voice tree, 24 in the member
    list, 30 in the self card, 36 next to a chat message. */
export type AvatarSize = 20 | 24 | 30 | 36;

export interface AvatarProps {
  size?: AvatarSize;
  /** Index into AVATAR_TINTS; wraps, so any user id hash works. */
  tint: number;
  initials?: string;
  /** Renders the prototype's striped stand-in used when a user has a picture. */
  photo?: boolean;
  /** Drives the speech halo. Leave it out where a face cannot speak - a chat
      message head - and the ring is never mounted at all. */
  speaking?: boolean;
  /** The self card uses a slightly wider halo (inset -5 instead of -4). */
  self?: boolean;
  /** Staggers the speech halo by 260ms per row, as the prototype does with the
      list index, so neighbouring halos do not pulse in lockstep. */
  haloIndex?: number;
  className?: string;
  title?: string;
}

/* Initials keep the prototype's per-size mono cap heights. */
const initialsSize: Record<AvatarSize, number> = { 20: 9, 24: 10, 30: 10, 36: 11 };

const SPEAKING_SHADOW = '0 0 0 2px var(--speak), 0 0 var(--glow-spread) -1px var(--speak-halo)';
const RESTING_SHADOW = '0 0 0 1px color-mix(in oklab, black 12%, transparent)';

/** A face and nothing else. Speech is the one state it carries, because speech
 *  is the one state that is about the face; mute and deafen belong to the row,
 *  after the name (components/ui/VoiceStateIcon.tsx). */
export function Avatar({
  size = 24,
  tint,
  initials,
  photo = false,
  speaking = false,
  self = false,
  haloIndex = 0,
  className,
  title,
}: AvatarProps) {
  // The halo ring is an infinite CSS animation, and a paused one still costs a
  // live animation object per avatar - measurable on an unbounded list like the
  // chat, where an avatar has no `speaking` prop and the ring can never show.
  // So it is mounted on the first phrase and kept from then on: nothing has to
  // fade out before someone has spoken, and everything does after.
  const [everSpoke, setEverSpoke] = useState(speaking);
  if (speaking && !everSpoke) setEverSpoke(true);

  const hex = AVATAR_TINTS[((tint % AVATAR_TINTS.length) + AVATAR_TINTS.length) % AVATAR_TINTS.length];
  const background = photo
    ? `repeating-linear-gradient(135deg, ${hex} 0 4px, color-mix(in oklab, ${hex}, white 12%) 4px 8px)`
    : hex;

  const style: CSSProperties = {
    width: size,
    height: size,
    background,
    boxShadow: speaking ? SPEAKING_SHADOW : RESTING_SHADOW,
  };

  return (
    <span
      title={title}
      className={cx(
        'relative grid flex-none place-items-center rounded-pill',
        'transition-[box-shadow] duration-[var(--t-mid)] ease-[var(--e-out)]',
        className,
      )}
      style={style}
    >
      {/* Kept mounted once it has been shown: the halo fades out when a phrase
          ends, and that needs an element to fade (styles/base.css). */}
      {everSpoke && (
        <span
          aria-hidden="true"
          data-speaking={speaking}
          className="gul-halo pointer-events-none absolute rounded-pill"
          style={{ inset: self ? -5 : -4 }}
        >
          <span className="gul-halo-ring" style={{ animationDelay: `${haloIndex * 260}ms` }} />
        </span>
      )}

      {initials && (
        <span
          className="font-mono font-medium text-on-accent"
          style={{ fontSize: initialsSize[size], letterSpacing: '.02em', lineHeight: 1 }}
        >
          {initials}
        </span>
      )}
    </span>
  );
}
