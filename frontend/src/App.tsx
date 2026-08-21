import { useEffect } from 'react';
import { useGulStore } from './state/store';
import { subscribeGulEvents } from './state/events';
import { ConnectScreen } from './app/ConnectScreen';
import { MainScreen } from './app/MainScreen';
import { TofuDialog } from './components/TofuDialog';
import { Settings } from './components/Settings';
import { ErrorBoundary } from './components/ErrorBoundary';

function App() {
  const state = useGulStore((s) => s.status.state);
  const tofu = useGulStore((s) => s.tofu);
  const settingsOpen = useGulStore((s) => s.settingsOpen);

  useEffect(() => {
    subscribeGulEvents();
  }, []);

  // The Go state machine decides what we show: an unexpected drop keeps the
  // session in 'reconnecting' (main screen, locked); terminal failures land
  // in 'disconnected' with an error for the connect form.
  const showMain = state === 'connected' || state === 'reconnecting';

  return (
    <ErrorBoundary>
      {showMain ? <MainScreen /> : <ConnectScreen />}
      {/* Modals live outside the screens: the main grid dims itself while
          reconnecting, and a dialog must stay usable on top of that. */}
      {settingsOpen && <Settings />}
      {tofu && <TofuDialog prompt={tofu} />}
    </ErrorBoundary>
  );
}

export default App;
