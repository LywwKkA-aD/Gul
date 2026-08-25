import { useEffect, type ReactNode } from 'react';
import { KeyboardIcon } from '@phosphor-icons/react/dist/csr/Keyboard';
import {
  AudioService,
  SettingsService,
} from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import type { GateMode } from '../state/gate';
import {
  VAD_HANGOVER_CHOICES,
  VAD_OPEN_MAX,
  VAD_OPEN_MIN,
  clampHangoverMs,
  clampOpenThreshold,
  keyLabel,
} from '../state/gate';
import { normalizeSettings } from '../state/settings';
import { globalPTTRunning } from '../state/hotkey';
import type { HotkeyMode, HotkeyStatus } from '../state/hotkey';
import { cx } from './ui/cx';
import { captionClass, selectClass } from './ui/controlStyles';

/* The transmit gate (PLAN.md 4.3), split across the two settings tabs the way
   a user looks for it: what opens the microphone is a keyboard question and
   lives in "Клавиши"; how loud speech has to be before the gate agrees is a
   sound question and stays in "Звук". Both halves keep writing through the
   same services - core applies the change to the engine and persists it
   (config.json) - so the split is a matter of composition only. */

/** "Клавиши": the transmit mode, the push-to-talk key, and whether that key
    is watched system wide. */
export function TransmitSettings() {
  const gateMode = useGulStore((s) => s.gateMode);
  const setGateMode = useGulStore((s) => s.setGateMode);
  const setHotkey = useGulStore((s) => s.setHotkey);

  const apply = (mode: GateMode) => {
    if (mode === gateMode) return;
    setGateMode(mode);
    // Only push-to-talk watches a key system wide, so the mode decides
    // whether the watch runs at all - and entering it is where binding the
    // stored key can fail (internal/core/voice.go SetGateMode).
    AudioService.SetGateMode(mode)
      .then(() => refreshHotkey(setHotkey))
      .catch((e: unknown) => console.error('gate mode:', e));
  };

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-2">
        <span className={captionClass}>Режим передачи</span>
        <div
          role="group"
          aria-label="Режим передачи"
          className="inline-flex w-fit rounded-md bg-bg-3 p-0.5 shadow-[var(--sh-sm)]"
        >
          <ModeButton active={gateMode === 'vad'} onClick={() => apply('vad')}>
            Активация по голосу
          </ModeButton>
          <ModeButton active={gateMode === 'ptt'} onClick={() => apply('ptt')}>
            Push-to-talk
          </ModeButton>
        </div>
        <p className="text-sm text-text-2">
          {gateMode === 'vad'
            ? 'Микрофон открывается сам, когда вы говорите. Порог и задержка — на вкладке «Звук». Клавиша ниже вступит в силу, если переключиться на push-to-talk.'
            : 'Микрофон открыт, пока зажата клавиша.'}
        </p>
      </div>

      {/* The key is shown in both modes. It is a setting, not live state:
          a tab named "Клавиши" that shows no key in the mode the app starts
          in is a dead end, and hiding it also made the dialog collapse and
          jump on every tab switch. The note above says when it applies. */}
      <PttKeyField />
      <GlobalPttField />
    </div>
  );
}

/** "Звук": the two numbers that decide when voice activation opens and closes
    the gate. They are persisted settings, not live state, so they are shown in
    either mode - only the note below changes. */
export function VadSettings() {
  const gateMode = useGulStore((s) => s.gateMode);
  const vadOpen = useGulStore((s) => s.vadOpen);
  const vadHangoverMs = useGulStore((s) => s.vadHangoverMs);
  const setVadTuning = useGulStore((s) => s.setVadTuning);

  // One slider drives both edges of the hysteresis band: the closing
  // threshold is derived in Go (config.CloseThreshold), not a second decision
  // for the user and not a second number to keep in sync here.
  const apply = (open: number, hangoverMs: number) => {
    const nextOpen = clampOpenThreshold(open);
    const nextHangover = clampHangoverMs(hangoverMs);
    setVadTuning(nextOpen, nextHangover);
    AudioService.SetVADTuning(nextOpen, nextHangover).catch(console.error);
  };

  const percent = Math.round(vadOpen * 100);

  return (
    <>
      {gateMode === 'ptt' && (
        <p className="text-sm text-text-2">
          Сейчас выбран push-to-talk, и эти две настройки ни на что не влияют. Они запомнены и
          вступят в силу, когда на вкладке «Клавиши» вернётся активация по голосу.
        </p>
      )}

      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between gap-2">
          <span className={captionClass}>Порог срабатывания</span>
          <span className="font-mono text-xs text-text-1">{percent} %</span>
        </div>
        <input
          type="range"
          min={Math.round(VAD_OPEN_MIN * 100)}
          max={Math.round(VAD_OPEN_MAX * 100)}
          step={1}
          value={percent}
          onChange={(e) => apply(Number(e.target.value) / 100, vadHangoverMs)}
          aria-label="Порог срабатывания"
          className="h-[18px] w-full"
        />
        <p className="text-sm text-text-2">
          Насколько уверенно шумодав должен услышать речь, чтобы открыть микрофон. Выше — меньше
          лишнего в эфире, но тихий голос рискует не пройти.
        </p>
      </div>

      <div className="flex flex-col gap-2">
        <span className={captionClass}>Задержка отключения</span>
        <select
          className={selectClass}
          value={vadHangoverMs}
          onChange={(e) => apply(vadOpen, Number(e.target.value))}
          aria-label="Задержка отключения"
        >
          {VAD_HANGOVER_CHOICES.map((ms) => (
            <option key={ms} value={ms}>
              {ms} мс
            </option>
          ))}
        </select>
        <p className="text-sm text-text-2">
          Сколько микрофон остаётся открытым после последнего звука речи. Пауза внутри фразы не
          должна её обрывать.
        </p>
      </div>
    </>
  );
}

/** Reads the hotkey status back after a change that can re-point the watch.
    Whether the stored key could actually be bound is decided in Go and only
    reported through the snapshot (services.Settings.Hotkey). */
function refreshHotkey(setHotkey: (hotkey: HotkeyStatus) => void): void {
  SettingsService.Load()
    .then((settings) => setHotkey(normalizeSettings(settings).hotkey))
    .catch((e: unknown) => console.error('settings:', e));
}

/* Prototype segStyle: 28px tall, 6px radius, accent fill when selected. */
function ModeButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cx(
        'h-7 flex-none cursor-pointer rounded-[6px] border-0 px-3 text-sm',
        'transition-[background-color,color] duration-[var(--t-fast)] ease-[var(--e-out)]',
        active
          ? 'bg-accent font-medium text-on-accent'
          : 'bg-transparent text-text-2 hover:text-text-1',
      )}
    >
      {children}
    </button>
  );
}

function PttKeyField() {
  const pttKey = useGulStore((s) => s.pttKey);
  const capturing = useGulStore((s) => s.pttCapturing);
  const setPttKey = useGulStore((s) => s.setPttKey);
  const setPttCapturing = useGulStore((s) => s.setPttCapturing);
  const setHotkey = useGulStore((s) => s.setHotkey);
  const watched = useGulStore((s) => globalPTTRunning(s.globalPtt, s.hotkey));

  // Capture phase: the key being bound must not reach the Escape handler of
  // the modal, a focused button, or the push-to-talk listener itself.
  useEffect(() => {
    if (!capturing) return;
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      setPttCapturing(false);
      // Escape leaves the current binding alone.
      if (!e.code || e.code === 'Escape') return;
      setPttKey(e.code);
      // A new key re-points the global watch, and this build's key vocabulary
      // is wider there than here: a perfectly valid binding can have no
      // global form, which only the status says.
      SettingsService.SetPTTKey(e.code)
        .then(() => refreshHotkey(setHotkey))
        .catch((err: unknown) => console.error('ptt key:', err));
    };
    const onBlur = () => setPttCapturing(false);
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('blur', onBlur);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('blur', onBlur);
    };
  }, [capturing, setPttKey, setPttCapturing, setHotkey]);

  // Closing the modal mid-capture must not leave the listener standing down.
  useEffect(() => () => setPttCapturing(false), [setPttCapturing]);

  return (
    <div className="flex flex-col gap-2">
      <span className={captionClass}>Клавиша PTT</span>
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-md bg-bg-3 px-3 py-2">
        <span className="flex min-w-0 items-center gap-2 text-sm text-text-2">
          <KeyboardIcon size={16} className="flex-none text-text-3" />
          <span className="min-w-0 truncate">
            {capturing ? 'Нажмите клавишу, Esc — отмена' : 'Удерживайте, чтобы говорить'}
          </span>
        </span>
        <button
          type="button"
          onClick={() => setPttCapturing(!capturing)}
          aria-label="Назначить клавишу push-to-talk"
          className={cx(
            'h-[22px] flex-none cursor-pointer rounded-sm border-0 px-2 font-mono text-xs',
            'whitespace-nowrap transition-[background-color,color,box-shadow]',
            'duration-[var(--t-fast)] ease-[var(--e-out)]',
            capturing
              ? 'bg-[var(--accent-weak)] text-text-1 shadow-[0_0_0_1px_var(--accent-line)]'
              : 'bg-transparent text-text-3 hover:bg-bg-4 hover:text-text-1',
          )}
        >
          {capturing ? 'Ждём…' : keyLabel(pttKey)}
        </button>
      </div>
      <p className="text-sm text-text-2">
        {watched
          ? 'Работает и когда окно не в фокусе. Клавиша запоминается между запусками.'
          : 'Работает, пока окно в фокусе. Клавиша запоминается между запусками.'}
      </p>
    </div>
  );
}

/* What the settings screen says about a mode that has nothing of its own to
   say. The Go side sends a ready Russian reason whenever the behaviour is not
   plain hold-to-talk (internal/hotkey), and these cover the rest. */
const HOTKEY_NOTES: Record<HotkeyMode, string> = {
  hold: 'Клавиша читается системно: удерживайте её, чтобы говорить, даже когда окно свёрнуто.',
  toggle: 'Доступен только режим переключения: нажатие включает передачу, повторное — выключает.',
  unsupported: 'Эта система не даёт следить за клавишей вне окна.',
};

function GlobalPttField() {
  const globalPtt = useGulStore((s) => s.globalPtt);
  const hotkey = useGulStore((s) => s.hotkey);
  const setGlobalPtt = useGulStore((s) => s.setGlobalPtt);
  const setHotkey = useGulStore((s) => s.setHotkey);

  const supported = hotkey.mode !== 'unsupported';

  const apply = (enabled: boolean) => {
    setGlobalPtt(enabled);
    SettingsService.SetGlobalPTT(enabled)
      .then(() => refreshHotkey(setHotkey))
      .catch((e: unknown) => console.error('global ptt:', e));
  };

  return (
    <div className="flex flex-col gap-2">
      <label
        className={cx(
          'flex items-center gap-2 text-sm text-text-2',
          supported ? 'cursor-pointer' : 'cursor-default opacity-70',
        )}
      >
        <input
          type="checkbox"
          role="switch"
          className="flex-none"
          checked={globalPtt}
          disabled={!supported}
          onChange={(e) => apply(e.target.checked)}
        />
        <span>Глобальная клавиша (работает, когда окно не в фокусе)</span>
      </label>
      <p className="text-sm text-text-2">{hotkey.reason || HOTKEY_NOTES[hotkey.mode]}</p>
      {hotkey.error !== '' && <p className="text-sm text-danger">{hotkey.error}</p>}
    </div>
  );
}
