import type { SavedServerInfo, ServerList } from '../shared/contracts.ts';
import { SavedServers } from './SavedServers.tsx';
import { selectedSavedServer, savedPasswordMessage } from './saved-login.ts';

export function ConnectPanel({
  address,
  username,
  password,
  remember,
  saved,
  busy,
  error,
  onAddress,
  onUsername,
  onPassword,
  onRemember,
  onChoose,
  onForget,
  onRefreshSaved,
  onConnect,
  onCancel,
}: {
  address: string;
  username: string;
  password: string;
  remember: boolean;
  saved: ServerList;
  busy: boolean;
  error: string;
  onAddress: (value: string) => void;
  onUsername: (value: string) => void;
  onPassword: (value: string) => void;
  onRemember: (value: boolean) => void;
  onChoose: (server: SavedServerInfo) => void;
  onForget: (address: string) => void;
  onRefreshSaved: () => void;
  onConnect: () => void;
  onCancel: () => void;
}) {
  const selected = selectedSavedServer(saved, address);
  const hasPassword = selected?.hasPassword === true;
  const passwordMessage = savedPasswordMessage(selected);
  return (
    <main className="connect-page">
      <section className="connect-card">
        <div className="brand-symbol">g</div>
        <h1>Заходи. Общайся.</h1>
        <p className="subtle">Голос, игры и экран — вместе.</p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            onConnect();
          }}
        >
          <label>
            Адрес сервера
            <input
              aria-label="Адрес сервера"
              value={address}
              onChange={(event) => onAddress(event.target.value)}
              placeholder="livekit+vless://…"
              required
              spellCheck={false}
              autoComplete="off"
              disabled={busy}
            />
          </label>
          <label>
            Твой ник
            <input
              aria-label="Твой ник"
              value={username}
              onChange={(event) => onUsername(event.target.value)}
              maxLength={64}
              required
              autoComplete="username"
              disabled={busy}
            />
          </label>
          <label>
            Пароль
            <input
              aria-label="Пароль"
              type="password"
              value={password}
              onChange={(event) => onPassword(event.target.value)}
              required={!hasPassword}
              placeholder={hasPassword ? 'Пароль сохранён — ввод не нужен' : ''}
              autoComplete="current-password"
              disabled={busy}
            />
          </label>
          {passwordMessage && (
            <p className="subtle" role="status">
              {passwordMessage}
            </p>
          )}
          {selected?.rememberPassword && !hasPassword && selected.passwordStatus !== 'missing' && (
            <button type="button" className="secondary-button" disabled={busy} onClick={onRefreshSaved}>
              Повторить чтение сохранённого пароля
            </button>
          )}
          <label className="checkbox-control">
            <input
              type="checkbox"
              checked={remember}
              disabled={busy || saved.storage !== 'protected'}
              onChange={(event) => onRemember(event.target.checked)}
            />
            Запомнить пароль на этом компьютере
          </label>
          {saved.storage === 'unavailable' && (
            <p className="subtle">
              Защищённое хранилище недоступно. Адрес и ник сохранятся, пароль потребуется при следующем входе.
            </p>
          )}
          <button className="primary" type="submit" disabled={busy}>
            {busy ? 'Подключаемся…' : 'Подключиться'}
          </button>
          {busy && (
            <button
              className="secondary-button"
              type="button"
              aria-label="Отменить подключение"
              onClick={onCancel}
            >
              Отменить подключение
            </button>
          )}
        </form>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <SavedServers servers={saved.servers} disabled={busy} onChoose={onChoose} onForget={onForget} />
      </section>
    </main>
  );
}
