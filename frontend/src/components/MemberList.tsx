import { useState, type CSSProperties, type ChangeEvent } from 'react';
import { SpeakerHighIcon } from '@phosphor-icons/react/dist/csr/SpeakerHigh';
import { AudioService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { useSpeaking } from '../state/speaking';
import type { ChannelNode, UserInfo } from '../state/types';
import {
  VOLUME_MAX,
  VOLUME_MIN,
  VOLUME_STEP,
  VOLUME_UNITY,
  initialsOf,
  tintFor,
} from '../state/types';
import { Avatar, Tooltip } from './ui';
import { TOOLTIP_DELAY_ROW_MS } from './ui/tooltipPosition';
import { cx } from './ui/cx';

// Nothing in this header is clickable, so it doubles as window drag surface on
// the platforms without a native title bar.
const DRAG_REGION = { '--wails-draggable': 'drag' } as CSSProperties;

export function MemberList({ channel }: { channel: ChannelNode | null }) {
  const users = (channel?.users ?? []).slice().sort((a, b) => a.name.localeCompare(b.name));

  return (
    <aside className="grid min-h-0 grid-rows-[var(--header-h)_minmax(0,1fr)] bg-bg-1 shadow-[-1px_0_0_var(--line)]">
      {/* Same height and rule as the channel header next to it: the two
          captions sit on one line across the window. A bare caption, as in the
          prototype (prototype-source.html:9917) - a number right-aligned at
          the far edge of a 220px panel reads as an orphan, so the count sits
          on the group heading below, next to the word it counts. */}
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
  const setUserVolume = useGulStore((s) => s.setUserVolume);

  // The engine keys per-user gain by the certificate hash, so it survives the
  // peer reconnecting. An anonymous peer has none - nothing to remember, no
  // slider. Our own stream never comes back to us either.
  const hash = user.hash ?? '';
  const adjustable = !user.isSelf && hash !== '';
  const volume = useGulStore((s) => s.userVolumes[hash] ?? VOLUME_UNITY);

  // Clicking the row pins the slider open; otherwise it follows the hover.
  const [pinned, setPinned] = useState(false);

  const onVolume = (e: ChangeEvent<HTMLInputElement>) => {
    const next = Number(e.target.value);
    setUserVolume(hash, next);
    AudioService.SetUserVolume(hash, next).catch(console.error);
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
        muted={user.selfMute}
        deaf={user.selfDeaf}
        self={user.isSelf}
        surface="light"
        haloIndex={index}
      />
      <span className="min-w-0 flex-1 truncate">{user.name}</span>
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
            aria-label={`Громкость: ${user.name}`}
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
          <SpeakerHighIcon size={13} className="shrink-0 text-text-3" />
          <input
            type="range"
            min={VOLUME_MIN}
            max={VOLUME_MAX}
            step={VOLUME_STEP}
            value={volume}
            onChange={onVolume}
            aria-label={`Персональная громкость: ${user.name}`}
            className="h-[18px] min-w-0 flex-1"
          />
          <span className="w-9 shrink-0 text-right font-mono text-xs tabular-nums text-text-3">
            {Math.round(volume * 100)}%
          </span>
        </div>
      )}
    </li>
  );
}
