import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import Styleguide from './components/dev/Styleguide'
import { SettingsService } from '../bindings/github.com/LywwKkA-aD/Gul/services'
import { normalizeSettings } from './state/settings'
import { useGulStore } from './state/store'
import './style.css'

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
  SettingsService.Load()
    .then((settings) => useGulStore.getState().applySettings(normalizeSettings(settings)))
    .catch((e: unknown) => console.error('settings:', e))
    .finally(render)
}
