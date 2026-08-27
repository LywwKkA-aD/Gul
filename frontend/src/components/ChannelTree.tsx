import { HashIcon } from '@phosphor-icons/react/dist/csr/Hash';
import { ChannelsService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { useSpeaking } from '../state/speaking';
import { useVoiceGates } from '../state/voiceGates';
import type { ChannelNode, UserInfo } from '../state/types';
import { initialsOf, tintFor } from '../state/types';
import { Avatar, Tooltip, VoiceStateIcon } from './ui';
import { TOOLTIP_DELAY_ROW_MS } from './ui/tooltipPosition';
import { cx } from './ui/cx';

/* The prototype insets its sidebar rows from the column edge (the scroll area
   is padded --s-2) and indents each level by 14px; a member sits one avatar
   further in than the channel it belongs to. Nothing here is full bleed: a
   selected channel is a rounded block with air around it. */
const ROW_INSET = 8;
const DEPTH_STEP = 14;
const MEMBER_INSET = 26;

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
          style={{ paddingLeft: `${ROW_INSET + depth * DEPTH_STEP}px`, height: 'var(--item-h)' }}
          className={cx(
            'mb-px flex w-full min-w-0 items-center gap-2 rounded-md pr-2 text-left text-sm',
            'transition-colors duration-[var(--t-fast)]',
            active
              ? 'bg-[var(--sb-active)] font-medium text-sb-text-1 shadow-[inset_2px_0_0_var(--speak)]'
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
  // Ours comes from the transmit gate, everyone else's from user:talking; the
  // halo is the same either way (state/speaking.ts).
  const speaking = useSpeaking(user);
  // Our own gates come from core, not from the tree: the tree learns of a
  // change only after the server has echoed it, and until then it would draw
  // the state the user has just left (state/voiceGatesRule.ts).
  const gates = useVoiceGates(user);

  return (
    <li
      style={{ paddingLeft: `${MEMBER_INSET + depth * DEPTH_STEP}px`, height: 'var(--item-h)' }}
      className={cx(
        'flex min-w-0 items-center gap-2 rounded-md pr-2 text-sm',
        'transition-colors duration-[var(--t-fast)] hover:bg-sb-2',
        // The prototype lifts a speaking name to the brightest sidebar text.
        speaking || user.isSelf ? 'text-sb-text-1' : 'text-sb-text-3',
      )}
    >
      <Avatar
        size={20}
        tint={tintFor(user)}
        initials={initialsOf(user.name)}
        speaking={speaking}
        self={user.isSelf}
        haloIndex={index}
      />
      <span className="min-w-0 flex-1 truncate">{user.name}</span>
      {/* Same order as the prototype's sidebar row: name, then the gates
          (prototype-source.html:9792). */}
      <VoiceStateIcon muted={gates.muted} deaf={gates.deaf} surface="sidebar" />
    </li>
  );
}
