import { useState, type CSSProperties, type ChangeEvent } from 'react';
import { SpeakerHighIcon } from '@phosphor-icons/react/dist/csr/SpeakerHigh';
import { SpeakerSimpleXIcon } from '@phosphor-icons/react/dist/csr/SpeakerSimpleX';
import { AudioService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { useSpeaking } from '../state/speaking';
import { useVoiceGates } from '../state/voiceGates';
import type { ChannelNode, UserInfo } from '../state/types';
import {
  VOLUME_MAX,
  VOLUME_MIN,
  VOLUME_STEP,
  VOLUME_UNITY,
  initialsOf,
  tintFor,
} from '../state/types';
import { Avatar, Tooltip, VoiceStateIcon } from './ui';
import { TOOLTIP_DELAY_ROW_MS } from './ui/tooltipPosition';
import { cx } from './ui/cx';

// Nothing in this header is clickable, so it doubles as window drag surface on
// the platforms without a native title bar.
const DRAG_REGION = { '--wails-draggable': 'drag' } as CSSProperties;

/** What a screen reader hears for one member row: the name, the gates that
    person closed, and whether we have silenced them here. The visible glyphs
    carry the same two facts, and they stay two facts: their microphone being
    off is theirs, our silence is ours. */
function memberRowLabel(
  user: UserInfo,
  gates: { muted: boolean; deaf: boolean },
  mutedHere: boolean,
): string {
  const state = gates.deaf
    ? ', звук выключен, микрофон тоже'
    : gates.muted
      ? ', микрофон выключен'
      : '';
  const local = mutedHere ? ', заглушён для вас' : '';
  return `Громкость: ${user.name}${state}${local}`;
}

export function MemberList({ channel }: { channel: ChannelNode | null }) {
  const users = (channel?.users ?? []).slice().sort((a, b) => a.name.localeCompare(b.name));

  return (
    <aside className="grid min-h-0 grid-rows-[var(--top-header-h)_minmax(0,1fr)] bg-bg-1 shadow-[-1px_0_0_var(--line)]">
      {/* Same height and rule as the channel header next to it: the two
          captions sit on one line across the window, and both start at the top
          edge - a caption is not a control, so it may sit inside the macOS
          drag band as long as it covers all of it (--top-header-h in
          styles/tokens.css). A bare caption, as in the prototype
          (prototype-source.html:9917) - a number right-aligned at the far edge
          of a 220px panel reads as an orphan, so the count sits on the group
          heading below, next to the word it counts. */}
      <header
        className="flex items-center px-3 text-xs tracking-[.08em] text-text-3 uppercase shadow-[0_1px_0_var(--line)]"
        style={DRAG_REGION}
      >
        <h3 className="min-w-0 flex-1 truncate">Участники</h3>
      </header>

      <div className="min-h-0 overflow-x-hidden overflow-y-auto px-2 py-3">
        <h4 className="px-2 pb-1 text-xs tracking-[.06em] text-text-3 uppercase">
          В канале — {users.length}
        </h4>
        <ul>
          {users.map((u, i) => (
            <MemberRow key={u.session} user={u} index={i} />
          ))}
        </ul>
      </div>
    </aside>
  );
}

function MemberRow({ user, index }: { user: UserInfo; index: number }) {
  // One indication, two sources: the transmit gate for us, user:talking for
  // everyone else (state/speaking.ts). Both selectors are booleans on purpose -
  // returning the Set would re-render the whole list on every gate transition.
  const speaking = useSpeaking(user);
  // Our own gates come from core, not from the tree: the tree learns of a
  // change only after the server has echoed it, and until then it would draw
  // the state the user has just left (state/voiceGatesRule.ts).
  const gates = useVoiceGates(user);
  const setUserVolume = useGulStore((s) => s.setUserVolume);

  // The engine keys per-user gain by whatever this client can call the peer:
  // the certificate hash when there is one, something weaker when there is not
  // (internal/mumble/peerkey.go). It used to be the hash alone, and the slider
  // was hidden whenever it was empty - which, once every session went
  // anonymous, meant hidden for everybody. Our own stream never comes back to
  // us, so the one row without controls is our own.
  const key = user.key;
  const adjustable = !user.isSelf && key !== '';
  const volume = useGulStore((s) => s.userVolumes[key] ?? VOLUME_UNITY);
  // Local mute is its own state next to the gain, keyed by the same key: the
  // engine keeps the gain while somebody is silenced and gives it back on
  // unmute (internal/audio/users.go). Nothing about this reaches the server.
  const mutedHere = useGulStore((s) => s.mutedUsers[key] === true);
  const setUserMuted = useGulStore((s) => s.setUserMuted);

  // Clicking the row pins the slider open; otherwise it follows the hover.
  const [pinned, setPinned] = useState(false);

  const onVolume = (e: ChangeEvent<HTMLInputElement>) => {
    const next = Number(e.target.value);
    setUserVolume(key, next);
    AudioService.SetUserVolume(key, next).catch(console.error);
  };

  const toggleMute = () => {
    const next = !mutedHere;
    setUserMuted(key, next);
    AudioService.SetUserMute(key, next).catch(console.error);
  };

  const rowClass = cx(
    'flex h-[var(--item-h)] w-full min-w-0 items-center gap-2 rounded-md border-0 bg-transparent',
    'px-2 text-left text-sm',
    speaking ? 'text-text-1' : 'text-text-2',
  );
  const row = (
    <>
      <Avatar
        size={24}
        tint={tintFor(user)}
        initials={initialsOf(user.name)}
        speaking={speaking}
        self={user.isSelf}
        haloIndex={index}
      />
      <span className={cx('min-w-0 flex-1 truncate', mutedHere && 'line-through opacity-60')}>
        {user.name}
      </span>
      {/* Two different facts, two different marks. VoiceStateIcon draws the
          gates THIS PERSON closed, in the danger scale; a person we silenced
          locally is our own doing and gets the muted foreground instead - a
          red glyph would read as something wrong with them. */}
      {mutedHere && (
        <SpeakerSimpleXIcon
          size={13}
          className="flex-none text-text-3"
          aria-hidden="true"
        />
      )}
      <VoiceStateIcon muted={gates.muted} deaf={gates.deaf} surface="light" />
    </>
  );

  return (
    <li className="group rounded-md transition-colors duration-[var(--t-fast)] hover:bg-bg-0">
      {adjustable ? (
        // The row is the only affordance for the per-user gain: the slider
        // below it is revealed by hover, which says nothing to a keyboard.
        <Tooltip
          label={pinned ? 'Скрыть громкость' : 'Громкость участника'}
          className="w-full"
          delayMs={TOOLTIP_DELAY_ROW_MS}
        >
          <button
            type="button"
            onClick={() => setPinned((p) => !p)}
            aria-expanded={pinned}
            // The button's label replaces its whole subtree for assistive
            // tech, so the glyph's own label would never be heard: the state
            // is spelled into the name instead.
            aria-label={memberRowLabel(user, gates, mutedHere)}
            className={cx(rowClass, 'cursor-pointer')}
          >
            {row}
          </button>
        </Tooltip>
      ) : (
        <div className={rowClass} title={user.name}>
          {row}
        </div>
      )}

      {adjustable && (
        <div
          className={cx(
            'items-center gap-2 px-2 pb-2',
            pinned ? 'flex' : 'hidden group-hover:flex',
          )}
        >
          {/* Silencing somebody is not their volume at zero: the slider keeps
              the value the user chose and it comes back on unmute, so the two
              controls sit side by side rather than one standing for both. */}
          <Tooltip label={mutedHere ? 'Вернуть звук' : 'Заглушить для себя'}>
            <button
              type="button"
              onClick={toggleMute}
              aria-pressed={mutedHere}
              aria-label={
                mutedHere ? `Вернуть звук: ${user.name}` : `Заглушить для себя: ${user.name}`
              }
              // Sized to the slider beside it rather than to the 28px control
              // grid: this is a control inside a row, not a row of its own.
              className={cx(
                'grid size-[18px] flex-none cursor-pointer place-items-center rounded-sm border-0',
                'bg-transparent transition-colors duration-[var(--t-fast)] hover:bg-bg-4',
                mutedHere ? 'text-text-1' : 'text-text-3',
              )}
            >
              {mutedHere ? <SpeakerSimpleXIcon size={13} /> : <SpeakerHighIcon size={13} />}
            </button>
          </Tooltip>
          <input
            type="range"
            min={VOLUME_MIN}
            max={VOLUME_MAX}
            step={VOLUME_STEP}
            value={volume}
            onChange={onVolume}
            // Not disabled while muted: the engine keeps this gain as what the
            // listener comes back at (internal/audio/users.go), so setting it now is
            // a legitimate thing to do before unmuting.
            
            aria-label={`Персональная громкость: ${user.name}`}
            className="h-[18px] min-w-0 flex-1 disabled:opacity-50"
          />
          <span
            className={cx(
              'w-9 shrink-0 text-right font-mono text-xs tabular-nums text-text-3',
              mutedHere && 'opacity-50',
            )}
          >
            {Math.round(volume * 100)}%
          </span>
        </div>
      )}
    </li>
  );
}
