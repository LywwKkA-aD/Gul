import { MicrophoneSlashIcon } from '@phosphor-icons/react/dist/csr/MicrophoneSlash';
import { SpeakerSlashIcon } from '@phosphor-icons/react/dist/csr/SpeakerSlash';
import { cx } from './cx';
import { voiceGlyph } from './voiceState';

/** Where the glyph is drawn; the two colour scales never mix (tokens.css). */
export type VoiceStateSurface = 'light' | 'sidebar';

export interface VoiceStateIconProps {
  muted: boolean;
  deaf: boolean;
  surface?: VoiceStateSurface;
  /** Prototype glyph size next to a name (prototype-source.html:9794). */
  size?: number;
  className?: string;
}

/**
 * The microphone / output gates of one participant, drawn after their name.
 *
 * Position: next to the name, not on the avatar. The prototype puts these
 * glyphs in the row (prototype-source.html:9794 and 9937) and the avatar
 * carries the speech halo alone - a badge on a 20px circle is unreadable and
 * fights the halo for the same pixels.
 *
 * Colour: the danger scale of the surface, as the prototype paints it
 * (prototype-source.html:9794 for the sidebar, :9937 for the member list).
 * A muted person is not an error, so the neutral foreground was tried first -
 * but the prototype is the design source of truth and it is right here for a
 * reason a mockup shows better than an argument: the glyph is 13px next to a
 * name, and in the neutral grey it disappears into the row.
 */
export function VoiceStateIcon({
  muted,
  deaf,
  surface = 'light',
  size = 13,
  className,
}: VoiceStateIconProps) {
  const glyph = voiceGlyph(muted, deaf);
  if (glyph === 'none') return null;

  const Icon = glyph === 'deaf' ? SpeakerSlashIcon : MicrophoneSlashIcon;
  // Neutral about who it describes: the same component labels a row in the
  // member list and our own card in the bottom bar. The deafened wording
  // names the microphone too, which is why there is only one glyph.
  const label = glyph === 'deaf' ? 'Звук выключен, микрофон тоже' : 'Микрофон выключен';

  return (
    <span
      role="img"
      aria-label={label}
      className={cx(
        'inline-flex flex-none items-center',
        surface === 'sidebar' ? 'text-[var(--sb-danger)]' : 'text-danger',
        className,
      )}
    >
      <Icon size={size} aria-hidden="true" />
    </span>
  );
}
