import { useEffect, type ReactNode } from 'react';
import { KeyboardIcon } from '@phosphor-icons/react/dist/csr/Keyboard';
import { AudioService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from '../state/store';
import type { GateMode } from '../state/gate';
import {
  VAD_HANGOVER_CHOICES,
  VAD_OPEN_MAX,
  VAD_OPEN_MIN,
  clampHangoverMs,
  clampOpenThreshold,
  closeThreshold,
  keyLabel,
} from '../state/gate';
import { cx } from './ui/cx';
import { captionClass, selectClass } from './ui/controlStyles';

/** Transmit gate section of the "Звук" tab: what opens the microphone
    (PLAN.md 4.3). The engine keeps these across its own restarts; storing
    them across application restarts is M4. */
export function GateSettings() {
  const gateMode = useGulStore((s) => s.gateMode);
  const setGateMode = useGulStore((s) => s.setGateMode);

  const apply = (mode: GateMode) => {
    if (mode === gateMode) return;
    setGateMode(mode);
    AudioService.SetGateMode(mode).catch(console.error);
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
      </div>

      {gateMode === 'vad' ? <VadTuning /> : <PttKeyField />}
    </div>
  );
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

function VadTuning() {
  const vadOpen = useGulStore((s) => s.vadOpen);
  const vadHangoverMs = useGulStore((s) => s.vadHangoverMs);
  const setVadTuning = useGulStore((s) => s.setVadTuning);

  // One slider drives both edges of the hysteresis band: the closing
  // threshold is a property of the gate, not a second decision for the user.
  const apply = (open: number, hangoverMs: number) => {
    const nextOpen = clampOpenThreshold(open);
    const nextHangover = clampHangoverMs(hangoverMs);
    setVadTuning(nextOpen, nextHangover);
    AudioService.SetVADTuning(nextOpen, closeThreshold(nextOpen), nextHangover).catch(
      console.error,
    );
  };

  const percent = Math.round(vadOpen * 100);

  return (
    <>
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

function PttKeyField() {
  const pttKey = useGulStore((s) => s.pttKey);
  const capturing = useGulStore((s) => s.pttCapturing);
  const setPttKey = useGulStore((s) => s.setPttKey);
  const setPttCapturing = useGulStore((s) => s.setPttCapturing);

  // Capture phase: the key being bound must not reach the Escape handler of
  // the modal, a focused button, or the push-to-talk listener itself.
  useEffect(() => {
    if (!capturing) return;
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      setPttCapturing(false);
      // Escape leaves the current binding alone.
      if (e.code && e.code !== 'Escape') setPttKey(e.code);
    };
    const onBlur = () => setPttCapturing(false);
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('blur', onBlur);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('blur', onBlur);
    };
  }, [capturing, setPttKey, setPttCapturing]);

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
        Работает, пока окно в фокусе. Глобальная клавиша появится в следующем милстоуне.
      </p>
    </div>
  );
}
