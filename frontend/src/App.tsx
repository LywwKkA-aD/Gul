import { useEffect } from 'react';
import { useGulStore } from './state/store';
import { subscribeGulEvents } from './state/events';
import { showsMainScreen } from './state/screen';
import { usePttSeed, usePushToTalk } from './state/ptt';
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

  // Window-level, so the key works from any screen - including while a modal
  // is open - and not only when the main screen holds focus.
  usePushToTalk();

  // The engine keeps transmitting a key held through a reconnect, and the
  // store drops the indicator on the way down - so the way back up asks the
  // engine what it is actually doing (state/pttSeed.ts).
  usePttSeed();

  // The Go state machine decides what we show: an unexpected drop keeps the
  // session in 'reconnecting' (main screen, locked); terminal failures land
  // in 'disconnected' with an error for the connect form, and an attempt that
  // has not connected yet stays 'connecting' on the form (state/screen.ts).
  const showMain = showsMainScreen(state);

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
