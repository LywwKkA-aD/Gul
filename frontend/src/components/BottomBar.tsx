import { MicrophoneIcon } from '@phosphor-icons/react/dist/csr/Microphone';
import { MicrophoneSlashIcon } from '@phosphor-icons/react/dist/csr/MicrophoneSlash';
import { HeadphonesIcon } from '@phosphor-icons/react/dist/csr/Headphones';
import { SpeakerSlashIcon } from '@phosphor-icons/react/dist/csr/SpeakerSlash';
import { GearSixIcon } from '@phosphor-icons/react/dist/csr/GearSix';
import { AudioService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { selfUser, useGulStore } from '../state/store';
import { initialsOf, tintFor } from '../state/types';
import { Avatar, IconButton, Tooltip } from './ui';
import { cx } from './ui/cx';

export function BottomBar() {
  const status = useGulStore((s) => s.status);
  const pingMs = useGulStore((s) => s.pingMs);
  const tree = useGulStore((s) => s.tree);
  const muted = useGulStore((s) => s.muted);
  const deafened = useGulStore((s) => s.deafened);
  const setVoiceGates = useGulStore((s) => s.setVoiceGates);
  const setSettingsOpen = useGulStore((s) => s.setSettingsOpen);
  const self = selfUser(tree);
  // Our own voice never comes back from the server, so the halo on this card
  // is driven by the transmit gate (audio:selftalking) and not by the talking
  // set - and the user watches this corner while speaking.
  const selfSpeaking = useGulStore((s) => s.selfTalking);
  // Push-to-talk gives no other feedback that the key actually reached the
  // gate, so the mic button carries it: filled and lit while the key is held.
  const transmitting = useGulStore((s) => s.gateMode === 'ptt' && s.pttHeld && !s.muted);

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

  return (
    // One row, as in the prototype (prototype-source.html:9802): identity on
    // the left with the ping under the nick, controls on the right, 46px tall.
    // Everything the sidebar does not spend here is channel list.
    <footer
      data-sidebar
      className={
        'grid shrink-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 bg-sb-0 ' +
        'py-2 pr-2 pl-3 shadow-[0_-1px_0_var(--sb-line)]'
      }
    >
      <div className="flex min-w-0 items-center gap-2">
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
        {/* leading on every line, not on the column: a Tailwind text-* utility
            carries its own line-height and would ignore an inherited one. The
            prototype's 1.25 is what keeps the two lines to the 30px of the
            avatar beside them, and the bar to 46px. */}
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-sm leading-[1.25] text-sb-text-1">
            {self?.user.name ?? '…'}
          </span>
          <span
            className={cx('truncate font-mono text-xs leading-[1.25] tabular-nums', pingTone)}
            title={latencyTitle}
          >
            {roundedPing === null ? '— мс' : `${roundedPing} мс`}
          </span>
        </span>
      </div>

      <div className="flex flex-none items-center gap-0.5">
        <Tooltip
          label={
            muted
              ? 'Включить микрофон'
              : transmitting
                ? 'Идёт передача, клавиша PTT зажата'
                : 'Выключить микрофон'
          }
        >
          <IconButton
            surface="sidebar"
            tone="danger"
            active={muted}
            onClick={toggleMic}
            aria-label={muted ? 'Включить микрофон' : 'Выключить микрофон'}
          >
            {muted ? (
              <MicrophoneSlashIcon size={16} weight="fill" />
            ) : (
              // The colour sits on the icon itself: a text utility on the
              // button would only compete with the IconButton's own class.
              <MicrophoneIcon
                size={16}
                weight={transmitting ? 'fill' : 'regular'}
                className={transmitting ? 'text-[var(--speak)]' : undefined}
              />
            )}
          </IconButton>
        </Tooltip>
        <Tooltip label={deafened ? 'Включить звук' : 'Выключить звук — не слышно никого'}>
          <IconButton
            surface="sidebar"
            tone="danger"
            active={deafened}
            onClick={toggleDeafen}
            aria-label={deafened ? 'Включить звук' : 'Выключить звук'}
          >
            {deafened ? <SpeakerSlashIcon size={16} weight="fill" /> : <HeadphonesIcon size={16} />}
          </IconButton>
        </Tooltip>
        <Tooltip label="Настройки">
          <IconButton surface="sidebar" onClick={() => setSettingsOpen(true)} aria-label="Настройки">
            <GearSixIcon size={16} />
          </IconButton>
        </Tooltip>
      </div>
    </footer>
  );
}
