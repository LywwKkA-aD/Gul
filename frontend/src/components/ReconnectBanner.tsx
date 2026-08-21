import { ConnectionService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { Button, Spinner } from './ui';

// Scenario C from the prototype: the UI beneath is locked (opacity + no
// pointer events); this banner is the only interactive element.
export function ReconnectBanner({ server }: { server: string }) {
  return (
    <div
      className="pointer-events-auto fixed inset-x-0 top-0 z-[var(--z-modal)] flex justify-center"
      style={{ animation: 'gul-in var(--t-mid) var(--e-out)' }}
    >
      <div className="mt-3 flex items-center gap-3 rounded-lg bg-bg-1 px-4 py-2.5 shadow-[var(--sh-lg)]">
        <Spinner size={14} />
        <div className="text-sm">
          <span className="text-text-1">Переподключение к </span>
          <span className="font-mono text-sm text-text-2">{server}</span>
          <span className="ml-2 text-xs text-warning">пинг — мс</span>
        </div>
        <Button variant="quiet" onClick={() => ConnectionService.Disconnect().catch(console.error)}>
          Отменить
        </Button>
      </div>
    </div>
  );
}
