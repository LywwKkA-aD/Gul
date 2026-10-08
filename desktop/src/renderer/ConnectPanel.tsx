import type { SavedServerInfo, ServerList } from '../shared/contracts.ts';
import { SavedServers } from './SavedServers.tsx';
import { selectedSavedServer, savedPasswordMessage } from './saved-login.ts';
import { GulLogo } from './GulLogo.tsx';

export function ConnectPanel({
  address,
  username,
  password,
  remember,
  saved,
  storageNotice,
  busy,
  error,
  onAddress,
  onUsername,
  onPassword,
  onRemember,
  onChoose,
  onForget,
  onRefreshSaved,
  onUnlockStorage,
  onOpenStorage,
  onConnect,
  onCancel,
}: {
  address: string;
  username: string;
  password: string;
  remember: boolean;
  saved: ServerList;
  storageNotice?: string;
  busy: boolean;
  error: string;
  onAddress: (value: string) => void;
  onUsername: (value: string) => void;
  onPassword: (value: string) => void;
  onRemember: (value: boolean) => void;
  onChoose: (server: SavedServerInfo) => void;
  onForget: (address: string) => void;
  onRefreshSaved: () => void;
  onUnlockStorage: () => void;
  onOpenStorage: () => void;
  onConnect: () => void;
  onCancel: () => void;
}) {
  const selected = selectedSavedServer(saved, address);
  const hasPassword = selected?.hasPassword === true;
  const passwordMessage = savedPasswordMessage(selected);
  return (
    <main className="connect-page">
      <section className="connect-card">
        <GulLogo className="brand-symbol" />
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
          {storageNotice && (
            <p className="subtle" role="status">
              {storageNotice}
            </p>
          )}
          {selected?.rememberPassword && !hasPassword && selected.passwordStatus !== 'missing' && (
            <button type="button" className="secondary-button" disabled={busy} onClick={onRefreshSaved}>
              Повторить чтение сохранённого пароля
            </button>
          )}
          {saved.passwordStorage &&
            saved.passwordStorage.provider !== 'other' &&
            ((!hasPassword && selected?.rememberPassword) ||
              saved.passwordStorage.state !== 'ready' ||
              saved.passwordStorage.restartRequired) && (
              <section className="password-storage-help" aria-label="Восстановление хранилища паролей">
                {saved.passwordStorage.state === 'locked' && (
                  <button
                    type="button"
                    className="secondary-button"
                    disabled={busy}
                    onClick={onUnlockStorage}
                  >
                    Разблокировать хранилище
                  </button>
                )}
                <button type="button" className="secondary-button" disabled={busy} onClick={onOpenStorage}>
                  Открыть «Пароли и ключи»
                </button>
                {saved.passwordStorage.state !== 'ready' && (
                  <p className="subtle">
                    В «Пароли и ключи» выберите связку «Вход» (Login) и нажмите значок замка, чтобы
                    разблокировать её. Нужен пароль этой связки: обычно он совпадает с паролем входа в Ubuntu.
                    Если связки нет, создайте защищённую связку паролей и назначьте её связкой по умолчанию.
                  </p>
                )}
                {(saved.passwordStorage.restartRequired || selected?.passwordStatus === 'unreadable') && (
                  <p className="subtle" role="status">
                    Полностью закройте Gul и запустите снова, чтобы повторить доступ к защищённому хранилищу.
                  </p>
                )}
              </section>
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
              Защищённое хранилище недоступно. Адрес и ник сохранятся; новый пароль пока нельзя запомнить.
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
