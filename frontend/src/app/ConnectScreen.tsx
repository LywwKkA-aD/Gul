import { useCallback, useEffect, useRef, useState } from 'react';
import { PlugsIcon } from '@phosphor-icons/react/dist/csr/Plugs';
import { KeyIcon } from '@phosphor-icons/react/dist/csr/Key';
import { XIcon } from '@phosphor-icons/react/dist/csr/X';
import { ConnectionService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { Button, Field, IconButton, Spinner, TextInput, Tooltip } from '../components/ui';
import { connectFallback, savedServerRows, type SavedServerRow } from './savedServers';

export function ConnectScreen() {
  const status = useGulStore((s) => s.status);
  // The form starts on what worked last time and nothing else: prefilling a
  // server the user never chose would point their password at a stranger.
  // Read once - the snapshot is in the store before the first render, and
  // after that the form owns what is in its fields.
  const [address, setAddress] = useState(() => useGulStore.getState().lastAddress);
  const [username, setUsername] = useState(() => useGulStore.getState().lastUsername);
  const [password, setPassword] = useState('');

  // The remembered servers, read from Go on mount. The list is small and only
  // changes when a connect is accepted or a row is forgotten, so it is fetched
  // rather than subscribed to.
  const [servers, setServers] = useState<SavedServerRow[]>([]);
  // Why the last click on a row did not connect. Russian, from Go.
  const [notice, setNotice] = useState('');
  const passwordRef = useRef<HTMLInputElement>(null);

  const connecting = status.state === 'connecting';
  const canConnect = !connecting && address.trim() !== '' && username.trim() !== '';

  const loadServers = useCallback(() => {
    ConnectionService.Servers()
      .then((list) => setServers(savedServerRows(list)))
      .catch((err: unknown) => console.error('servers:', err));
  }, []);

  useEffect(loadServers, [loadServers]);

  const connect = () => {
    if (!canConnect) return;
    setNotice('');
    // Remembered by the Go side, and only once the server accepts it
    // (internal/core/settings.go).
    // Result arrives via connection:state events; sync errors land there too.
    ConnectionService.Connect(address, username, password).catch(console.error);
  };

  // A click on a saved row connects in Go: the stored password is looked up
  // there and never crosses into this webview (internal/core/servers.go).
  const connectSaved = (row: SavedServerRow) => {
    if (connecting) return;
    setNotice('');
    ConnectionService.ConnectSaved(row.address)
      .then((result) => {
        const fallback = connectFallback(result);
        if (fallback.kind === 'none') return;
        setNotice(fallback.message);
        if (fallback.kind === 'refresh') {
          loadServers();
          return;
        }
        // The password could not be read, so the form takes over with
        // everything that is not a secret already filled in.
        setAddress(fallback.address);
        setUsername(fallback.username);
        setPassword('');
        passwordRef.current?.focus();
      })
      .catch((err: unknown) => console.error('connect saved:', err));
  };

  const forget = (row: SavedServerRow) => {
    setNotice('');
    // The row goes now: core drops the entry unconditionally and only reports
    // a password that survived, which is not a reason to keep showing a server
    // the user asked to be rid of.
    setServers((rows) => rows.filter((r) => r.address !== row.address));
    ConnectionService.ForgetServer(row.address)
      .catch((err: unknown) => console.error('forget server:', err))
      .finally(loadServers);
  };

  return (
    <main className="flex h-full items-center justify-center bg-bg-0">
      <div
        className="w-[360px] rounded-lg bg-bg-1 p-6 shadow-[var(--sh-md)]"
        style={{ animation: 'gul-in var(--t-slow) var(--e-out)' }}
      >
        <h1 className="mb-5 flex items-center gap-2 font-display text-[15px] tracking-wide text-text-1">
          <PlugsIcon size={18} weight="bold" />
          GUL
        </h1>

        {/* Nothing when there is nothing: a first run sees the form alone,
            with no empty box explaining that it is empty. */}
        {servers.length > 0 && (
          <section className="mb-5 flex flex-col gap-1">
            <h2 className="pb-1 text-xs tracking-[.08em] text-text-3 uppercase">Ваши серверы</h2>
            {servers.map((row) => (
              <SavedServerItem
                key={row.address}
                row={row}
                disabled={connecting}
                onConnect={() => connectSaved(row)}
                onForget={() => forget(row)}
              />
            ))}
          </section>
        )}

        <div className="space-y-4">
          <Field label="Сервер">
            <TextInput
              mono
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="127.0.0.1:64738 или wss://host/mumble"
              disabled={connecting}
              onKeyDown={(e) => e.key === 'Enter' && connect()}
            />
          </Field>
          <Field label="Ник">
            <TextInput
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="как вас звать"
              disabled={connecting}
              onKeyDown={(e) => e.key === 'Enter' && connect()}
            />
          </Field>
          <Field label="Пароль сервера (для WSS обязателен)">
            <TextInput
              ref={passwordRef}
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={connecting}
              onKeyDown={(e) => e.key === 'Enter' && connect()}
            />
          </Field>

          <Button size="lg" className="w-full" onClick={connect} disabled={!canConnect}>
            {connecting ? (
              <span className="inline-flex items-center gap-2">
                <Spinner size={14} /> Подключение…
              </span>
            ) : (
              'Подключиться'
            )}
          </Button>

          {/* Why a click on a saved server sent the user back to the form. Not
              a connection error: the attempt never started. */}
          {notice && (
            <p className="text-xs leading-relaxed text-warning" role="status">
              {notice}
            </p>
          )}

          {/* While the attempt is still alive the message is a wait, not a
              failure: a relay asking for N seconds is not a red error. */}
          {status.error && (
            <p
              className={'text-xs leading-relaxed ' + (connecting ? 'text-warning' : 'text-danger')}
              role="alert"
            >
              {status.error}
            </p>
          )}

          {/* A relay that is rate limiting or full answers with a wait and the
              attempt keeps retrying on its own, so this screen can stay in
              'connecting' for minutes. Leaving it has to be possible. */}
          {connecting && (
            <button
              type="button"
              className="mx-auto block cursor-pointer border-0 bg-transparent p-0 text-xs text-text-3 hover:underline"
              onClick={() => ConnectionService.Disconnect().catch(console.error)}
            >
              Отменить попытку
            </button>
          )}
        </div>
      </div>
    </main>
  );
}

/** One remembered server: click to connect, and a way to be rid of it.
 *
 *  Two sibling buttons rather than one with a control inside it - a button
 *  inside a button is not a thing a browser will draw. */
function SavedServerItem({
  row,
  disabled,
  onConnect,
  onForget,
}: {
  row: SavedServerRow;
  disabled: boolean;
  onConnect: () => void;
  onForget: () => void;
}) {
  return (
    <div className="group flex items-center gap-1 rounded-md transition-colors duration-[var(--t-fast)] hover:bg-bg-3">
      <Tooltip label={row.address} className="min-w-0 flex-1">
        <button
          type="button"
          onClick={onConnect}
          disabled={disabled}
          aria-label={`Подключиться: ${row.address}, ник ${row.username}${
            row.hasPassword ? ', пароль сохранён' : ''
          }`}
          className={
            'flex w-full min-w-0 cursor-pointer flex-col items-start gap-0.5 rounded-md ' +
            'border-0 bg-transparent px-2 py-1.5 text-left disabled:cursor-default disabled:opacity-50'
          }
        >
          <span className="w-full truncate font-mono text-sm text-text-1">{row.label}</span>
          <span className="flex w-full min-w-0 items-center gap-1.5 text-xs text-text-3">
            <span className="min-w-0 truncate">{row.username}</span>
            {row.hint && (
              <>
                {/* The key says "a click is enough" at a glance; the words
                    behind it are for a screen reader and a slower look. */}
                <KeyIcon size={11} className="shrink-0" aria-hidden="true" />
                <span className="shrink-0">{row.hint}</span>
              </>
            )}
          </span>
        </button>
      </Tooltip>
      {/* Visible on hover, and always to a keyboard: focus-within keeps it on
          screen while it is the focused element. */}
      <Tooltip label="Забыть сервер">
        <IconButton
          onClick={onForget}
          disabled={disabled}
          aria-label={`Забыть сервер ${row.address}`}
          className="mr-1 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
        >
          <XIcon size={13} />
        </IconButton>
      </Tooltip>
    </div>
  );
}
