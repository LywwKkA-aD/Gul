import { useState } from 'react';
import { PlugsIcon } from '@phosphor-icons/react/dist/csr/Plugs';
import { ConnectionService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { GUL_RELAY_ADDRESS } from '../state/settings';
import { Button, Field, Spinner, TextInput } from '../components/ui';

export function ConnectScreen() {
  const status = useGulStore((s) => s.status);
  // The form starts on what worked last time and nothing else: prefilling a
  // server the user never chose would point their password at a stranger.
  // Read once - the snapshot is in the store before the first render, and
  // after that the form owns what is in its fields.
  const [address, setAddress] = useState(() => useGulStore.getState().lastAddress);
  const [username, setUsername] = useState(() => useGulStore.getState().lastUsername);
  const [password, setPassword] = useState('');

  const connecting = status.state === 'connecting';
  const canConnect = !connecting && address.trim() !== '' && username.trim() !== '';

  const connect = () => {
    if (!canConnect) return;
    // Remembered by the Go side, and only once the server accepts it
    // (internal/core/settings.go).
    // Result arrives via connection:state events; sync errors land there too.
    ConnectionService.Connect(address, username, password).catch(console.error);
  };

  return (
    <main className="fixed inset-0 flex items-center justify-center bg-bg-0">
      <div
        className="w-[360px] rounded-lg bg-bg-1 p-6 shadow-[var(--sh-md)]"
        style={{ animation: 'gul-in var(--t-slow) var(--e-out)' }}
      >
        <h1 className="mb-5 flex items-center gap-2 font-display text-[15px] tracking-wide text-text-1">
          <PlugsIcon size={18} weight="bold" />
          GUL
        </h1>

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
          <button
            type="button"
            className="-mt-2 cursor-pointer border-0 bg-transparent p-0 text-xs text-[var(--accent-text)] hover:underline disabled:cursor-default disabled:opacity-50"
            onClick={() => setAddress(GUL_RELAY_ADDRESS)}
            disabled={connecting}
          >
            Подставить сервер Gul
          </button>
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
