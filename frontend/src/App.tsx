import { useState } from 'react';
import { PlugsConnectedIcon, PlugsIcon } from '@phosphor-icons/react';
import { ConnectionService } from '../bindings/gul/services';

// M0 placeholder screen: connect to a server, watch the Go log for the
// channel tree. The real Connect screen with design tokens lands in M1.
function App() {
  const [address, setAddress] = useState('127.0.0.1:64738');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [state, setState] = useState('disconnected');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const refreshState = () => ConnectionService.State().then(setState).catch(console.error);

  const connect = async () => {
    setBusy(true);
    setError('');
    try {
      await ConnectionService.Connect(address, username, password);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
      void refreshState();
    }
  };

  const disconnect = async () => {
    setBusy(true);
    try {
      await ConnectionService.Disconnect();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
      void refreshState();
    }
  };

  const connected = state !== 'disconnected';

  return (
    <main className="flex min-h-screen items-center justify-center bg-white text-slate-900">
      <div className="w-80 space-y-3">
        <h1 className="flex items-center gap-2 text-lg font-medium">
          {connected ? <PlugsConnectedIcon size={20} weight="fill" /> : <PlugsIcon size={20} />}
          Gul
          <span className="ml-auto text-xs text-slate-500">{state}</span>
        </h1>
        <input
          className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm"
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          placeholder="server:port"
          disabled={busy || connected}
        />
        <input
          className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder="nickname"
          disabled={busy || connected}
        />
        <input
          className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="password (optional)"
          disabled={busy || connected}
        />
        {connected ? (
          <button
            className="w-full rounded-lg bg-slate-200 py-2 text-sm font-medium disabled:opacity-50"
            onClick={disconnect}
            disabled={busy}
          >
            Disconnect
          </button>
        ) : (
          <button
            className="w-full rounded-lg bg-blue-700 py-2 text-sm font-medium text-white disabled:opacity-50"
            onClick={connect}
            disabled={busy || !username}
          >
            Connect
          </button>
        )}
        {error && <p className="text-xs text-red-600">{error}</p>}
        <p className="text-xs text-slate-400">
          M0: channel tree is printed to the application log.
        </p>
      </div>
    </main>
  );
}

export default App;
