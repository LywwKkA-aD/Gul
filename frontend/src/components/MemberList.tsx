import type { ChannelNode } from '../state/types';
import { initialsOf, tintFor } from '../state/types';
import { Avatar } from './ui';

export function MemberList({ channel }: { channel: ChannelNode | null }) {
  const users = (channel?.users ?? []).slice().sort((a, b) => a.name.localeCompare(b.name));

  return (
    <aside className="w-[220px] min-w-0 overflow-y-auto bg-bg-1 px-3 py-3 shadow-[-1px_0_0_var(--line)]">
      <h3 className="mb-2 px-1 text-xs font-medium uppercase tracking-[.08em] text-text-3">
        Участники — {users.length}
      </h3>
      <ul className="space-y-0.5">
        {users.map((u) => (
          <li
            key={u.session}
            className="flex min-w-0 items-center gap-2 rounded-md px-1 py-1 text-sm text-text-2 hover:bg-bg-0"
          >
            <Avatar
              size={24}
              tint={tintFor(u)}
              initials={initialsOf(u.name)}
              muted={u.selfMute}
              deaf={u.selfDeaf}
              surface="light"
            />
            <span className="min-w-0 flex-1 truncate">{u.name}</span>
          </li>
        ))}
      </ul>
    </aside>
  );
}
