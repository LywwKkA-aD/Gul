import { ShieldWarningIcon } from '@phosphor-icons/react/dist/csr/ShieldWarning';
import { ConnectionService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import type { TofuPrompt } from '../state/types';
import { Button } from './ui';

// Shown when a known server presents a new certificate. Accepting re-pins the
// fingerprint and the manager retries on its own; declining just closes the
// dialog and the connection stays down.
export function TofuDialog({ prompt }: { prompt: TofuPrompt }) {
  const setTofu = useGulStore((s) => s.setTofu);

  const accept = () => {
    ConnectionService.AcceptFingerprint().catch(console.error);
    setTofu(null);
  };

  return (
    // The top padding clears the band macOS gives to its traffic lights: a
    // click there drags the window instead of reaching the dialog (tokens.css).
    <div className="fixed inset-0 z-[var(--z-modal)] flex items-center justify-center p-6 pt-[calc(var(--titlebar-h)+var(--s-6))] bg-[color-mix(in_oklab,var(--sb-0)_45%,transparent)]">
      <div
        role="alertdialog"
        aria-labelledby="tofu-title"
        className="w-[440px] rounded-lg bg-bg-1 p-5 shadow-[var(--sh-lg)]"
        style={{ animation: 'gul-modal var(--t-slow) var(--e-out)' }}
      >
        <h2 id="tofu-title" className="mb-2 flex items-center gap-2 text-sm font-medium text-text-1">
          <ShieldWarningIcon size={18} className="text-warning" />
          Сертификат сервера изменился
        </h2>
        <p className="mb-3 text-sm leading-relaxed text-text-2">
          Сервер <span className="font-mono text-sm">{prompt.server}</span> предъявил не тот
          сертификат, который был сохранён при первом подключении. Так бывает при переустановке
          сервера — или при подмене соединения. Продолжайте, только если доверяете этой смене.
        </p>
        <div className="mb-4 space-y-1 rounded-md bg-bg-3 p-3 font-mono text-[11px] leading-relaxed text-text-3">
          <p className="break-all">был: {prompt.oldFingerprint}</p>
          <p className="break-all text-text-2">стал: {prompt.newFingerprint}</p>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="quiet" onClick={() => setTofu(null)}>
            Отменить
          </Button>
          <Button onClick={accept}>Доверять и подключиться</Button>
        </div>
      </div>
    </div>
  );
}
