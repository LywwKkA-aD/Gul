import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import Styleguide from './components/dev/Styleguide'
import { AudioService, SettingsService } from '../bindings/github.com/LywwKkA-aD/Gul/services'
import { normalizeSettings } from './state/settings'
import { useGulStore } from './state/store'
import { markPlatform } from './app/platform'
import './style.css'

// Before anything is measured or painted: on macOS the window has no title
// bar of its own and the traffic lights sit on top of the page, so the layout
// has to know which platform it is on (styles/tokens.css, --titlebar-h).
markPlatform()

// Dev-only design-system showcase: open the app with #styleguide in the URL.
const styleguide = window.location.hash === '#styleguide'
const Root = styleguide ? Styleguide : App

function render() {
  ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
    <React.StrictMode>
      <Root />
    </React.StrictMode>,
  )
}

if (styleguide) {
  render()
} else {
  // The persisted settings seed the connect form and the settings modal, and
  // both read them once on mount - so they have to be in the store before the
  // first render. A snapshot that never arrives costs the convenience, not the
  // start: the store keeps its defaults and the UI renders anyway.
  const seedSettings = SettingsService.Load()
    .then((snapshot) => useGulStore.getState().applySettings(normalizeSettings(snapshot)))
    .catch((e: unknown) => console.error('settings:', e))

  // Mute and deafen live in core and are reachable from the system tray, so
  // the first paint asks instead of assuming. Later changes arrive as
  // audio:self events (state/events.ts).
  const seedSelfAudio = AudioService.SelfState()
    .then((state) => useGulStore.getState().setVoiceGates(state.muted, state.deafened))
    .catch((e: unknown) => console.error('self audio:', e))

  // Same reason, for the push-to-talk indicator: a window that opens onto a
  // live session - or a global key already held - must not paint a resting
  // microphone over an open gate. Later changes arrive as audio:ptt, and the
  // reconnect case is handled by usePttSeed (state/ptt.ts).
  const seedPtt = AudioService.PTTState()
    .then((ptt) => useGulStore.getState().setPttHeld(ptt.held === true))
    .catch((e: unknown) => console.error('ptt state:', e))

  Promise.all([seedSettings, seedSelfAudio, seedPtt]).finally(render)
}
