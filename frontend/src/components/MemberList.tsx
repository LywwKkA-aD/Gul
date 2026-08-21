import { useState, type ChangeEvent } from 'react';
import { SpeakerHighIcon } from '@phosphor-icons/react/dist/csr/SpeakerHigh';
import { AudioService } from '../../bindings/gul/services';
import { useGulStore } from '../state/store';
import type { ChannelNode, UserInfo } from '../state/types';
import {
  VOLUME_MAX,
  VOLUME_MIN,
  VOLUME_STEP,
  VOLUME_UNITY,
  initialsOf,
  tintFor,
} from '../state/types';
import { Avatar } from './ui';
import { cx } from './ui/cx';

export function MemberList({ channel }: { channel: ChannelNode | null }) {
  const users = (channel?.users ?? []).slice().sort((a, b) => a.name.localeCompare(b.name));

  return (
    <aside className="w-[220px] min-w-0 overflow-y-auto bg-bg-1 px-3 py-3 shadow-[-1px_0_0_var(--line)]">
      <h3 className="mb-2 px-1 text-xs font-medium uppercase tracking-[.08em] text-text-3">
        Участники — {users.length}
      </h3>
      <ul className="space-y-0.5">
        {users.map((u, i) => (
          <MemberRow key={u.session} user={u} index={i} />
        ))}
      </ul>
    </aside>
  );
}

function MemberRow({ user, index }: { user: UserInfo; index: number }) {
  // Boolean selector on purpose: the talking Set is replaced on every gate
  // transition, so returning it (or anything derived) would re-render the
  // whole list - and an unstable reference would loop useSyncExternalStore.
  const speaking = useGulStore((s) => s.talkingSessions.has(user.session));
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
    'flex w-full min-w-0 items-center gap-2 rounded-md border-0 bg-transparent px-1 py-1 text-left text-sm',
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
        <button
          type="button"
          onClick={() => setPinned((p) => !p)}
          aria-expanded={pinned}
          title={`Громкость: ${user.name}`}
          className={cx(rowClass, 'cursor-pointer')}
        >
          {row}
        </button>
      ) : (
        <div className={rowClass} title={user.name}>
          {row}
        </div>
      )}

      {adjustable && (
        <div
          className={cx(
            'items-center gap-2 px-1 pb-1.5',
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
          <span className="w-9 shrink-0 text-right font-mono text-xs text-text-3">
            {Math.round(volume * 100)}%
          </span>
        </div>
      )}
    </li>
  );
}
