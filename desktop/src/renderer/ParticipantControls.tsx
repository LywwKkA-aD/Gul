import type { UserInfo } from '../shared/contracts.ts';
import { Dialog } from './Dialog.tsx';
import { Icon } from './MediaElements.tsx';

export interface LocalAudioPreference {
  readonly gain: number;
  readonly muted: boolean;
}
export const defaultLocalAudio: LocalAudioPreference = Object.freeze({ gain: 1, muted: false });

export function ParticipantRow({
  user,
  self,
  speaking,
  locallyMuted,
  onOpen,
}: {
  user: UserInfo;
  self: boolean;
  speaking: boolean;
  locallyMuted: boolean;
  onOpen: (user: UserInfo) => void;
}) {
  return (
    <button
      className={`member ${speaking ? 'speaking' : ''} ${locallyMuted ? 'locally-muted' : ''}`}
      aria-label={self ? `Ваши настройки: ${user.name}` : `Настройки участника ${user.name}`}
      onClick={() => onOpen(user)}
      onContextMenu={(event) => {
        event.preventDefault();
        onOpen(user);
      }}
      title={self ? 'Открыть настройки' : 'Громкость и локальное отключение'}
    >
      <span className="avatar small">{user.name.slice(0, 1).toUpperCase()}</span>
      <span className="member-name">
        {user.name}
        <small>{self ? 'это вы' : ''}</small>
      </span>
      <span className="member-states">
        {(user.selfMute || user.selfDeaf) && (
          <span role="img" aria-label="Микрофон выключен" title="Микрофон выключен">
            <Icon name="mic" off />
          </span>
        )}
        {user.selfDeaf && (
          <span role="img" aria-label="Звук выключен" title="Звук выключен">
            <Icon name="deaf" off />
          </span>
        )}
        {locallyMuted && (
          <span role="img" aria-label="Вы выключили этого участника" title="Вы выключили этого участника">
            <Icon name="volumeOff" />
          </span>
        )}
      </span>
    </button>
  );
}

export function ParticipantControls({
  user,
  preference,
  onVolume,
  onMuted,
  onClose,
}: {
  user: UserInfo;
  preference: LocalAudioPreference;
  onVolume: (gain: number) => void;
  onMuted: (muted: boolean) => void;
  onClose: () => void;
}) {
  return (
    <Dialog title={`Настройки участника ${user.name}`} className="participant-dialog" onClose={onClose}>
      <div className="participant-heading">
        <span className="avatar">{user.name.slice(0, 1).toUpperCase()}</span>
        <h2>{user.name}</h2>
      </div>
      <label className="volume-control">
        <span>
          Громкость голоса <output>{Math.round(preference.gain * 100)}%</output>
        </span>
        <input
          aria-label={`Громкость ${user.name}`}
          type="range"
          min="0"
          max="2"
          step="0.05"
          value={preference.gain}
          onChange={(event) => onVolume(Number(event.target.value))}
        />
      </label>
      <label className="checkbox-control">
        <input
          type="checkbox"
          aria-label={`Выключить участника ${user.name}`}
          checked={preference.muted}
          onChange={(event) => onMuted(event.target.checked)}
        />
        Выключить для меня
      </label>
      <p className="subtle">Это меняет только звук у вас. Громкость демонстрации регулируется отдельно.</p>
    </Dialog>
  );
}
