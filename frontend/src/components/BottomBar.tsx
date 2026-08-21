import { useState } from 'react';
import { MicrophoneIcon } from '@phosphor-icons/react/dist/csr/Microphone';
import { MicrophoneSlashIcon } from '@phosphor-icons/react/dist/csr/MicrophoneSlash';
import { HeadphonesIcon } from '@phosphor-icons/react/dist/csr/Headphones';
import { SpeakerSlashIcon } from '@phosphor-icons/react/dist/csr/SpeakerSlash';
import { GearSixIcon } from '@phosphor-icons/react/dist/csr/GearSix';
import { LifebuoyIcon } from '@phosphor-icons/react/dist/csr/Lifebuoy';
import { AudioService, DiagnosticsService } from '../../bindings/gul/services';
import { selfUser, useGulStore } from '../state/store';
import { initialsOf, tintFor } from '../state/types';
import { Avatar, IconButton } from './ui';

export function BottomBar() {
  const status = useGulStore((s) => s.status);
  const pingMs = useGulStore((s) => s.pingMs);
  const tree = useGulStore((s) => s.tree);
  const muted = useGulStore((s) => s.muted);
  const deafened = useGulStore((s) => s.deafened);
  const setVoiceGates = useGulStore((s) => s.setVoiceGates);
  const setSettingsOpen = useGulStore((s) => s.setSettingsOpen);
  const self = selfUser(tree);
  const selfSpeaking = useGulStore((s) => (self ? s.talkingSessions.has(self.user.session) : false));
  const [diagPath, setDiagPath] = useState<string | null>(null);

  const connected = status.state === 'connected';
  const roundedPing = connected && pingMs !== null ? Math.max(0, Math.round(pingMs)) : null;
  const pingTone =
    roundedPing === null
      ? 'text-sb-text-3'
      : roundedPing <= 150
        ? 'text-sb-text-3'
        : roundedPing <= 300
          ? 'text-warning'
          : 'text-[var(--sb-danger)]';
  const connectionLabel = connected
    ? 'в сети'
    : status.state === 'reconnecting'
      ? 'переподключение'
      : status.state;
  const latencyTitle =
    roundedPing === null
      ? 'Ожидаем первый замер RTT до сервера'
      : `RTT до сервера по текущему TLS/TCP-сеансу: ${roundedPing} мс`;

  // Both gates are pushed on every toggle: they are independent switches in
  // the engine, and re-sending an unchanged one is a no-op there.
  const applyGates = (nextMuted: boolean, nextDeafened: boolean) => {
    setVoiceGates(nextMuted, nextDeafened);
    AudioService.SetMute(nextMuted).catch(console.error);
    AudioService.SetDeafen(nextDeafened).catch(console.error);
  };

  // Talking into ears that hear nothing makes no sense, so opening the mic
  // lifts the deafen as well - the behaviour everyone knows from Discord.
  const toggleMic = () => applyGates(!muted, muted ? false : deafened);
  // Deafen implies a closed mic; lifting it opens the mic back up.
  const toggleDeafen = () => applyGates(!deafened, !deafened);

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
            speaking={selfSpeaking}
            muted={muted}
            deaf={deafened}
            self
            surface="sidebar"
          />
        ) : null}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-sb-text-1">{self?.user.name ?? '…'}</p>
        </div>
        <div className="flex flex-none items-center gap-0.5">
          <IconButton
            surface="sidebar"
            tone="danger"
            active={muted}
            onClick={toggleMic}
            title={muted ? 'Включить микрофон' : 'Выключить микрофон'}
            aria-label={muted ? 'Включить микрофон' : 'Выключить микрофон'}
          >
            {muted ? <MicrophoneSlashIcon size={16} weight="fill" /> : <MicrophoneIcon size={16} />}
          </IconButton>
          <IconButton
            surface="sidebar"
            tone="danger"
            active={deafened}
            onClick={toggleDeafen}
            title={deafened ? 'Включить звук' : 'Выключить звук'}
            aria-label={deafened ? 'Включить звук' : 'Выключить звук'}
          >
            {deafened ? <SpeakerSlashIcon size={16} weight="fill" /> : <HeadphonesIcon size={16} />}
          </IconButton>
          <IconButton
            surface="sidebar"
            onClick={() => setSettingsOpen(true)}
            title="Настройки"
            aria-label="Настройки"
          >
            <GearSixIcon size={16} />
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
      </div>
      <div className="mt-1.5 flex items-center justify-between gap-2 pl-10 text-xs">
        <span className="min-w-0 truncate text-sb-text-3">{connectionLabel}</span>
        <span className={`flex-none font-mono tabular-nums ${pingTone}`} title={latencyTitle}>
          {roundedPing === null ? '— мс' : `${roundedPing} мс`}
        </span>
      </div>
      {diagPath && (
        <p className="mt-2 break-all font-mono text-[10px] leading-snug text-sb-text-3">
          Диагностика: {diagPath}
        </p>
      )}
    </footer>
  );
}
