import { useState } from 'react';
import { PlugsIcon } from '@phosphor-icons/react/dist/csr/Plugs';
import { ConnectionService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { Button, Field, Spinner, TextInput } from '../components/ui';

export function ConnectScreen() {
  const status = useGulStore((s) => s.status);
  const [address, setAddress] = useState('wss://murmur.gulvox.com/mumble');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  const connecting = status.state === 'connecting';

  const connect = () => {
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
              placeholder="host:64738 или wss://host/mumble"
              disabled={connecting}
            />
          </Field>
          <Field label="Ник">
            <TextInput
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="как вас звать"
              disabled={connecting}
              onKeyDown={(e) => e.key === 'Enter' && username && connect()}
            />
          </Field>
          <Field label="Пароль сервера (для WSS обязателен)">
            <TextInput
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={connecting}
              onKeyDown={(e) => e.key === 'Enter' && username && connect()}
            />
          </Field>

          <Button size="lg" className="w-full" onClick={connect} disabled={connecting || !username.trim()}>
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
