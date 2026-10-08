import type { SavedServerInfo } from '../shared/contracts.ts';
import { Icon } from './MediaElements.tsx';

export function SavedServers({
  servers,
  disabled,
  onChoose,
  onForget,
}: {
  servers: readonly SavedServerInfo[];
  disabled: boolean;
  onChoose: (server: SavedServerInfo) => void;
  onForget: (address: string) => void;
}) {
  if (!servers.length) return null;
  const hostname = (address: string) => {
    try {
      return new URL(address).hostname;
    } catch {
      return 'Сервер';
    }
  };
  return (
    <section className="saved-servers" aria-label="Сохранённые серверы">
      <h2>Недавние серверы</h2>
      {servers.map((server) => (
        <div className="saved-server" key={server.address}>
          <button
            type="button"
            disabled={disabled}
            onClick={() => onChoose(server)}
            aria-label={`Выбрать сервер ${hostname(server.address)}`}
          >
            <span>{hostname(server.address)}</span>
            <small>
              {server.username}
              {server.hasPassword
                ? ' · пароль сохранён'
                : server.passwordStatus === 'locked'
                  ? ' · хранилище заблокировано'
                  : server.passwordStatus === 'unreadable'
                    ? ' · пароль недоступен'
                    : server.passwordStatus === 'save-failed'
                      ? ' · пароль не сохранён'
                      : ''}
            </small>
          </button>
          <button
            type="button"
            className="icon-button"
            disabled={disabled}
            aria-label={`Удалить сервер ${hostname(server.address)}`}
            onClick={() => onForget(server.address)}
          >
            <Icon name="close" />
          </button>
        </div>
      ))}
    </section>
  );
}
