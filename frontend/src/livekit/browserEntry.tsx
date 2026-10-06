import { createRoot } from 'react-dom/client';
import { BrowserScreen } from './BrowserScreen';
import { openBrowserSession } from './browserSession';
import '../style.css';

// Fragments are never sent in HTTP requests. Remove the one-use code before any
// asynchronous work; the exchanged bearer stays only inside session closures.
const code = location.hash.slice(1);
history.replaceState(null, '', location.pathname);
const root = createRoot(document.getElementById('root')!);
let active = true;
const closing = () => { active = false; };
window.addEventListener('pagehide', closing, { once: true });
root.render(<p className="p-6 text-sm text-text-3">Подключаем демонстрации…</p>);
void openBrowserSession(code).then((session) => {
  if (!active) { void session.close(); return; }
  root.render(<BrowserScreen session={session} />);
}).catch(() => {
  root.render(<main className="p-6 text-sm text-text-1"><p role="alert">Эта ссылка больше не действует. Откройте демонстрации снова из Gul.</p></main>);
});
