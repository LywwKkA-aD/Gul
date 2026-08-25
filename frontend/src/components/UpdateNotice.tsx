import { Browser } from '@wailsio/runtime';
import { ArrowSquareOutIcon } from '@phosphor-icons/react/dist/csr/ArrowSquareOut';
import { XIcon } from '@phosphor-icons/react/dist/csr/X';
import { UpdateService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import { IconButton, Tooltip } from './ui';

/**
 * One line, at the bottom of the window, when a newer release exists.
 *
 * It is a strip in the layout rather than a floating card on purpose: nothing
 * it can cover, nothing to move out of the way, and it disappears the moment
 * it is dismissed. There is no automatic update behind it - the link opens the
 * release page in the user's browser and the rest is theirs (PLAN.md 7 M4+).
 *
 * Dismissing is per version and remembered on the Go side, so the same release
 * never asks twice and the next one still can.
 */
export function UpdateNotice() {
  const update = useGulStore((s) => s.update);
  const setUpdate = useGulStore((s) => s.setUpdate);
  if (!update) return null;

  const dismiss = () => {
    // The line goes now; the record of it is the Go side's business.
    setUpdate(null);
    UpdateService.Dismiss(update.tag).catch((err: unknown) => console.error('dismiss:', err));
  };

  return (
    <aside
      // It appears when the check finishes, long after the page settled, so
      // it announces itself rather than waiting to be found on a tab pass.
      role="status"
      aria-live="polite"
      className="flex h-8 shrink-0 items-center gap-2 bg-bg-0 px-3 text-xs text-text-2 shadow-[0_-1px_0_var(--line)]"
      style={{ animation: 'gul-in var(--t-mid) var(--e-out)' }}
    >
      <span className="min-w-0 truncate">
        Доступна версия <span className="font-mono">{update.version}</span>
      </span>
      {/* Opened in the user's own browser: the webview is the application, not
          a place to read release notes in. */}
      <button
        type="button"
        onClick={() => Browser.OpenURL(update.url).catch((err: unknown) => console.error(err))}
        className="inline-flex flex-none cursor-pointer items-center gap-1 border-0 bg-transparent p-0 text-[var(--accent-text)] hover:underline"
      >
        Что нового
        <ArrowSquareOutIcon size={12} aria-hidden="true" />
      </button>
      <span className="flex-1" />
      <Tooltip label="Больше не напоминать об этой версии">
        <IconButton onClick={dismiss} aria-label={`Скрыть сообщение о версии ${update.version}`}>
          <XIcon size={13} />
        </IconButton>
      </Tooltip>
    </aside>
  );
}
