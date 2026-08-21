import { useState } from 'react';
import { MicrophoneIcon } from '@phosphor-icons/react/dist/csr/Microphone';
import { HeadphonesIcon } from '@phosphor-icons/react/dist/csr/Headphones';
import { LifebuoyIcon } from '@phosphor-icons/react/dist/csr/Lifebuoy';
import { DiagnosticsService } from '../../bindings/gul/services';
import { selfUser, useGulStore } from '../state/store';
import { initialsOf, tintFor } from '../state/types';
import { Avatar, IconButton } from './ui';

export function BottomBar() {
  const status = useGulStore((s) => s.status);
  const tree = useGulStore((s) => s.tree);
  const self = selfUser(tree);
  const [diagPath, setDiagPath] = useState<string | null>(null);

  const collectDiagnostics = () => {
    DiagnosticsService.Collect()
      .then((path) => {
        setDiagPath(path);
        setTimeout(() => setDiagPath(null), 6000);
      })
      .catch(console.error);
  };

  return (
    <footer data-sidebar className="shrink-0 bg-sb-0 px-3 py-2.5">
      <div className="flex items-center gap-2.5">
        {self ? (
          <Avatar
            size={30}
            tint={tintFor(self.user)}
            initials={initialsOf(self.user.name)}
            self
            surface="sidebar"
          />
        ) : null}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-sb-text-1">{self?.user.name ?? '…'}</p>
          <p className="truncate text-xs text-sb-text-3">{status.state === 'connected' ? 'в сети' : status.state}</p>
        </div>
        <IconButton surface="sidebar" disabled title="Микрофон — в M2 (голоса ещё нет)" aria-label="Микрофон">
          <MicrophoneIcon size={16} />
        </IconButton>
        <IconButton surface="sidebar" disabled title="Звук — в M2 (голоса ещё нет)" aria-label="Звук">
          <HeadphonesIcon size={16} />
        </IconButton>
        <IconButton
          surface="sidebar"
          onClick={collectDiagnostics}
          title="Собрать диагностику (логи и версия в zip)"
          aria-label="Диагностика"
        >
          <LifebuoyIcon size={16} />
        </IconButton>
      </div>
      {diagPath && (
        <p className="mt-2 break-all font-mono text-[10px] leading-snug text-sb-text-3">
          Диагностика: {diagPath}
        </p>
      )}
    </footer>
  );
}
