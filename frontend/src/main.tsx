import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import Styleguide from './components/dev/Styleguide'
import './style.css'

// Dev-only design-system showcase: open the app with #styleguide in the URL.
const Root = window.location.hash === '#styleguide' ? Styleguide : App

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <Root />
  </React.StrictMode>,
)
