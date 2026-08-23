import { HashIcon } from '@phosphor-icons/react/dist/csr/Hash';
import { ChannelsService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import type { ChannelNode, UserInfo } from '../state/types';
import { initialsOf, tintFor } from '../state/types';
import { Avatar, Tooltip } from './ui';
import { TOOLTIP_DELAY_ROW_MS } from './ui/tooltipPosition';
import { cx } from './ui/cx';

export function ChannelTree() {
  const tree = useGulStore((s) => s.tree);
  if (!tree) return null;

  // The server root renders its children at top level; joining the root is
  // still possible through its own row.
  return (
    <nav aria-label="Каналы">
      <ChannelRow channel={tree} depth={0} />
    </nav>
  );
}

function ChannelRow({ channel, depth }: { channel: ChannelNode; depth: number }) {
  const activeChannelId = useGulStore((s) => s.activeChannelId);
  const active = activeChannelId === channel.id;

  const join = () => {
    if (!active) ChannelsService.Join(channel.id).catch(console.error);
  };

  const sorted = [...channel.children].sort((a, b) => a.position - b.position || a.name.localeCompare(b.name));

  return (
    <div>
      <Tooltip
        label={active ? `Вы здесь: ${channel.name}` : `Перейти в «${channel.name}»`}
        className="w-full"
        delayMs={TOOLTIP_DELAY_ROW_MS}
      >
        <button
          type="button"
          onClick={join}
          onDoubleClick={join}
          style={{ paddingLeft: `${12 + depth * 14}px`, height: 'var(--item-h)' }}
          className={cx(
            'flex w-full min-w-0 items-center gap-2 rounded-md pr-2 text-left text-sm transition-colors duration-[var(--t-fast)]',
            active
              ? 'bg-[var(--sb-active)] text-sb-text-1'
              : 'text-sb-text-2 hover:bg-sb-2 hover:text-sb-text-1',
          )}
        >
          <HashIcon size={14} className="shrink-0 opacity-70" />
          <span className="min-w-0 flex-1 truncate">{channel.name}</span>
        </button>
      </Tooltip>

      {channel.users.length > 0 && (
        <ul className="mb-1">
          {channel.users
            .slice()
            .sort((a, b) => a.name.localeCompare(b.name))
            .map((u, i) => (
              <UserRow key={u.session} user={u} depth={depth} index={i} />
            ))}
        </ul>
      )}

      {sorted.map((child) => (
        <ChannelRow key={child.id} channel={child} depth={depth + 1} />
      ))}
    </div>
  );
}

function UserRow({ user, depth, index }: { user: UserInfo; depth: number; index: number }) {
  // A boolean selector: the Set itself is replaced on every transition, but
  // this stays referentially stable, so the row only re-renders on its own
  // gate. Never return the Set (or a derived array) from here.
  const speaking = useGulStore((s) => s.talkingSessions.has(user.session));

  return (
    <li
      style={{ paddingLeft: `${12 + depth * 14 + 20}px`, height: 'var(--item-h)' }}
      className={cx(
        'flex min-w-0 items-center gap-2 pr-2 text-sm transition-colors duration-[var(--t-fast)]',
        // The prototype lifts a speaking name to the brightest sidebar text.
        speaking || user.isSelf ? 'text-sb-text-1' : 'text-sb-text-3',
      )}
    >
      <Avatar
        size={20}
        tint={tintFor(user)}
        initials={initialsOf(user.name)}
        speaking={speaking}
        muted={user.selfMute}
        deaf={user.selfDeaf}
        self={user.isSelf}
        surface="sidebar"
        haloIndex={index}
      />
      <span className="min-w-0 flex-1 truncate">{user.name}</span>
    </li>
  );
}
