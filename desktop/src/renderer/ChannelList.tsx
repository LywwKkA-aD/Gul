import type { AudioState, ChannelNode, UserInfo } from '../shared/contracts.ts';
import { ParticipantRow, type LocalAudioPreference } from './ParticipantControls.tsx';
import { Icon } from './MediaElements.tsx';

export function flattenChannels(tree: ChannelNode | undefined): readonly ChannelNode[] {
  return tree ? [tree, ...(tree.children ?? []).flatMap(flattenChannels)] : [];
}

export function ChannelList({
  tree,
  selected,
  selfSession,
  selfAudio,
  busy,
  speakers,
  localAudio,
  onChannel,
  onUser,
}: {
  tree: ChannelNode | undefined;
  selected: number;
  selfSession: number;
  selfAudio: AudioState;
  busy: boolean;
  speakers: readonly string[];
  localAudio: Readonly<Record<string, LocalAudioPreference>>;
  onChannel: (id: number) => void;
  onUser: (user: UserInfo) => void;
}) {
  const branch = (channel: ChannelNode, depth: number) => (
    <li className="channel-branch" key={channel.id}>
      <button
        className={`channel ${channel.id === selected ? 'selected' : ''}`}
        aria-label={channel.name}
        aria-current={channel.id === selected ? 'true' : undefined}
        disabled={busy}
        onClick={() => onChannel(channel.id)}
        style={{ paddingLeft: 12 + Math.min(depth, 5) * 12 }}
      >
        <Icon name="voice" />
        <span className="channel-name">{channel.name}</span>
        <small>{channel.users?.length || ''}</small>
      </button>
      {!!channel.users?.length && (
        <div className="channel-users" style={{ paddingLeft: 16 + Math.min(depth, 5) * 12 }}>
          {channel.users.map((user) => (
            <ParticipantRow
              key={user.session}
              user={
                user.session === selfSession
                  ? { ...user, selfMute: selfAudio.muted || selfAudio.deafened, selfDeaf: selfAudio.deafened }
                  : user
              }
              self={user.session === selfSession}
              speaking={speakers.includes(`voice.${user.session}`)}
              locallyMuted={localAudio[`voice.${user.session}`]?.muted ?? false}
              onOpen={onUser}
            />
          ))}
        </div>
      )}
      {!!channel.children?.length && <ul>{channel.children.map((child) => branch(child, depth + 1))}</ul>}
    </li>
  );
  return (
    <nav aria-label="Голосовые каналы">
      <ul className="channel-tree">{tree && branch(tree, 0)}</ul>
    </nav>
  );
}
