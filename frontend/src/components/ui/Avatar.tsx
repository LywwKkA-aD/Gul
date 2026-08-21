import { MicrophoneSlashIcon } from '@phosphor-icons/react/dist/csr/MicrophoneSlash';
import { SpeakerSlashIcon } from '@phosphor-icons/react/dist/csr/SpeakerSlash';
import type { CSSProperties } from 'react';
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
  speaking?: boolean;
  muted?: boolean;
  deaf?: boolean;
  /** The self card uses a slightly wider halo (inset -5 instead of -4). */
  self?: boolean;
  /** Picks the danger colour and the badge backdrop for the surface below. */
  surface?: 'light' | 'sidebar';
  /** Staggers the speech halo by 260ms per row, as the prototype does with the
      list index, so neighbouring halos do not pulse in lockstep. */
  haloIndex?: number;
  className?: string;
  title?: string;
}

/* Initials keep the prototype's per-size mono cap heights. */
const initialsSize: Record<AvatarSize, number> = { 20: 9, 24: 10, 30: 10, 36: 11 };

/* Mute / deaf chips are not in the prototype markup (there they sit next to the
   name in the row); sized here to stay legible down to a 20px avatar. */
const badgeSize: Record<AvatarSize, { box: number; icon: number }> = {
  20: { box: 12, icon: 8 },
  24: { box: 13, icon: 9 },
  30: { box: 15, icon: 10 },
  36: { box: 17, icon: 12 },
};

const SPEAKING_SHADOW = '0 0 0 2px var(--speak), 0 0 var(--glow-spread) -1px var(--speak-halo)';
const RESTING_SHADOW = '0 0 0 1px color-mix(in oklab, black 12%, transparent)';

export function Avatar({
  size = 24,
  tint,
  initials,
  photo = false,
  speaking = false,
  muted = false,
  deaf = false,
  self = false,
  surface = 'light',
  haloIndex = 0,
  className,
  title,
}: AvatarProps) {
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

  const badge = badgeSize[size];
  const badgeStyle: CSSProperties = {
    width: badge.box,
    height: badge.box,
    background: surface === 'sidebar' ? 'var(--sb-1)' : 'var(--bg-1)',
    color: surface === 'sidebar' ? 'var(--sb-danger)' : 'var(--danger)',
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
      {speaking && (
        <span
          aria-hidden="true"
          className="animate-halo pointer-events-none absolute rounded-pill border border-solid border-speak"
          style={{ inset: self ? -5 : -4, animationDelay: `${haloIndex * 260}ms` }}
        />
      )}

      {initials && (
        <span
          className="font-mono font-medium text-on-accent"
          style={{ fontSize: initialsSize[size], letterSpacing: '.02em', lineHeight: 1 }}
        >
          {initials}
        </span>
      )}

      {muted && (
        <span
          aria-label="микрофон выключен"
          role="img"
          className="absolute -right-px -bottom-px grid place-items-center rounded-pill"
          style={badgeStyle}
        >
          <MicrophoneSlashIcon size={badge.icon} weight="fill" />
        </span>
      )}

      {deaf && (
        <span
          aria-label="звук выключен"
          role="img"
          className="absolute -bottom-px -left-px grid place-items-center rounded-pill"
          style={badgeStyle}
        >
          <SpeakerSlashIcon size={badge.icon} weight="fill" />
        </span>
      )}
    </span>
  );
}
