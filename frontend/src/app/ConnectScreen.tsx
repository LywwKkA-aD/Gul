import { useState } from 'react';
import { PlugsIcon } from '@phosphor-icons/react/dist/csr/Plugs';
import { ConnectionService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { GUL_RELAY_ADDRESS, loadLastConnection, rememberAttempt } from '../state/lastConnection';
import { Button, Field, Spinner, TextInput } from '../components/ui';

export function ConnectScreen() {
  const status = useGulStore((s) => s.status);
  // The form starts on what worked last time and nothing else: prefilling a
  // server the user never chose would point their password at a stranger.
  const [remembered] = useState(loadLastConnection);
  const [address, setAddress] = useState(remembered.address);
  const [username, setUsername] = useState(remembered.username);
  const [password, setPassword] = useState('');

  const connecting = status.state === 'connecting';
  const canConnect = !connecting && address.trim() !== '' && username.trim() !== '';

  const connect = () => {
    if (!canConnect) return;
    // Stored only once the server accepts it (state/lastConnection.ts).
    rememberAttempt({ address, username });
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

          {status.error && (
            <p className="text-xs leading-relaxed text-danger" role="alert">
              {status.error}
            </p>
          )}
        </div>
      </div>
    </main>
  );
}
