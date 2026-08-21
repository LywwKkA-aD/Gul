import { useEffect, useRef, useState, type ChangeEvent, type ReactNode } from 'react';
import { SlidersHorizontalIcon } from '@phosphor-icons/react/dist/csr/SlidersHorizontal';
import { KeyboardIcon } from '@phosphor-icons/react/dist/csr/Keyboard';
import { PaletteIcon } from '@phosphor-icons/react/dist/csr/Palette';
import { XIcon } from '@phosphor-icons/react/dist/csr/X';
import { AudioService } from '../../bindings/gul/services';
import { useGulStore } from '../state/store';
import type { AudioDevice } from '../state/types';
import { SILENT_DB, meterPercent } from '../state/types';
import { Field } from './ui';
import { cx } from './ui/cx';

/** Settings modal. M2 ships the "Звук" tab only; the rest of the prototype's
    tabs stay in place as disabled stubs so the layout does not move later. */
export function Settings() {
  const setSettingsOpen = useGulStore((s) => s.setSettingsOpen);
  const dialogRef = useRef<HTMLDivElement | null>(null);

  const close = () => setSettingsOpen(false);

  // Escape closes from anywhere, including when focus never entered the modal.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setSettingsOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [setSettingsOpen]);

  useEffect(() => {
    dialogRef.current?.focus();
  }, []);

  return (
    <div
      onClick={(e) => {
        if (e.target === e.currentTarget) close();
      }}
      className={
        'animate-in fixed inset-0 z-[var(--z-modal)] grid place-items-center p-6 ' +
        'bg-[color-mix(in_oklab,var(--sb-0)_44%,transparent)]'
      }
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label="Настройки"
        tabIndex={-1}
        className={
          'animate-modal grid max-h-[min(560px,100%)] w-[min(720px,100%)] ' +
          'grid-rows-[auto_minmax(0,1fr)] overflow-hidden rounded-lg bg-bg-1 shadow-[var(--sh-lg)]'
        }
      >
        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 pt-4 pb-3">
          <span className="font-display text-[13px] font-medium tracking-[.06em]">НАСТРОЙКИ</span>
          <button
            type="button"
            onClick={close}
            aria-label="Закрыть"
            className={
              'grid size-7 flex-none cursor-pointer place-items-center rounded-md border-0 ' +
              'bg-transparent text-text-2 transition-[background-color,color] ' +
              'duration-[var(--t-fast)] ease-[var(--e-out)] hover:bg-bg-3 hover:text-text-1'
            }
          >
            <XIcon size={16} />
          </button>
        </div>

        <div className="grid min-h-0 grid-cols-[168px_minmax(0,1fr)]">
          <div className="flex min-h-0 flex-col gap-0.5 pr-2 pb-4 pl-3">
            <Tab active icon={<SlidersHorizontalIcon size={15} />}>
              Звук
            </Tab>
            <Tab icon={<KeyboardIcon size={15} />}>Клавиши</Tab>
            <Tab icon={<PaletteIcon size={15} />}>Внешний вид</Tab>
          </div>

          <div className="min-h-0 overflow-x-hidden overflow-y-auto border-l border-line px-4 pb-4">
            <AudioTab />
          </div>
        </div>
      </div>
    </div>
  );
}

function Tab({
  active = false,
  icon,
  children,
}: {
  active?: boolean;
  icon: ReactNode;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      disabled={!active}
      aria-current={active ? 'page' : undefined}
      title={active ? undefined : 'Появится в следующих милстоунах'}
      className={cx(
        'flex h-8 cursor-pointer items-center gap-2 rounded-md border-0 px-3 text-left text-sm',
        'transition-[background-color,color] duration-[var(--t-fast)] ease-[var(--e-out)]',
        'disabled:cursor-default disabled:opacity-50 disabled:hover:bg-transparent',
        active
          ? 'bg-[var(--accent-weak)] font-medium text-text-1'
          : 'bg-transparent text-text-2 hover:bg-bg-3 hover:text-text-1',
      )}
    >
      <span className="flex-none">{icon}</span>
      <span className="min-w-0 truncate">{children}</span>
    </button>
  );
}

function AudioTab() {
  const captureId = useGulStore((s) => s.captureId);
  const playbackId = useGulStore((s) => s.playbackId);
  const setDevices = useGulStore((s) => s.setDevices);

  const [capture, setCapture] = useState<AudioDevice[]>([]);
  const [playback, setPlayback] = useState<AudioDevice[]>([]);
  const [failed, setFailed] = useState(false);

  // One enumeration per open: the device list is a snapshot, and the engine
  // watchdog is what reacts to hot-plugs while running.
  useEffect(() => {
    let alive = true;
    AudioService.Devices()
      .then((d) => {
        if (!alive) return;
        setCapture(d.capture ?? []);
        setPlayback(d.playback ?? []);
      })
      .catch((e: unknown) => {
        // Bridge errors carry no readable message; the detail lives in the log.
        console.error('audio devices:', e);
        if (alive) setFailed(true);
      });
    return () => {
      alive = false;
    };
  }, []);

  // "" keeps the system default; the engine restarts itself when running.
  const apply = (nextCapture: string, nextPlayback: string) => {
    setDevices(nextCapture, nextPlayback);
    AudioService.SelectDevices(nextCapture, nextPlayback).catch(console.error);
  };

  return (
    <div className="flex flex-col gap-5 pt-0.5">
      <Field label="Устройство ввода">
        <DeviceSelect
          devices={capture}
          value={captureId}
          onSelect={(id) => apply(id, playbackId)}
          ariaLabel="Устройство ввода"
        />
      </Field>

      <MicMeter />

      <Field label="Устройство вывода">
        <DeviceSelect
          devices={playback}
          value={playbackId}
          onSelect={(id) => apply(captureId, id)}
          ariaLabel="Устройство вывода"
        />
      </Field>

      {failed && (
        <p className="text-sm text-danger">
          Не удалось получить список устройств. Останется системное по умолчанию; подробности в
          логе (gul.log).
        </p>
      )}
    </div>
  );
}

/* Prototype selectStyle: the input base with the native chrome stripped -
   34px tall, no border, the 1px edge is a box-shadow ring. */
const selectClass =
  'h-[34px] w-full min-w-0 cursor-pointer appearance-none rounded-md border-0 bg-bg-1 px-3 ' +
  'text-text-1 shadow-[var(--sh-sm)] transition-[background-color,box-shadow] ' +
  'duration-[var(--t-fast)] ease-[var(--e-out)] hover:shadow-[0_0_0_1px_var(--text-3)] ' +
  'focus:shadow-[0_0_0_1px_var(--accent),0_0_0_3px_color-mix(in_oklab,var(--accent)_18%,transparent)]';

function DeviceSelect({
  devices,
  value,
  onSelect,
  ariaLabel,
}: {
  devices: AudioDevice[];
  value: string;
  onSelect: (id: string) => void;
  ariaLabel: string;
}) {
  // A device chosen earlier can be gone by now (unplugged, other machine).
  // Keep it listed instead of silently showing an empty select.
  const missing = value !== '' && !devices.some((d) => d.id === value);

  const onChange = (e: ChangeEvent<HTMLSelectElement>) => onSelect(e.target.value);

  return (
    <select className={selectClass} value={value} onChange={onChange} aria-label={ariaLabel}>
      <option value="">Системное по умолчанию</option>
      {devices.map((d) => (
        <option key={d.id} value={d.id}>
          {d.isDefault ? `${d.name} (системное)` : d.name}
        </option>
      ))}
      {missing && <option value={value}>Недоступное устройство</option>}
    </select>
  );
}

function MicMeter() {
  const micDb = useGulStore((s) => s.micDb);
  const percent = meterPercent(micDb);
  const silent = micDb <= SILENT_DB;

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs tracking-[.08em] text-text-3 uppercase">Тест микрофона</span>
        <span className="font-mono text-xs text-text-1">
          {silent ? '— dBFS' : `${micDb.toFixed(0)} dBFS`}
        </span>
      </div>
      <div
        className="relative h-2 overflow-hidden rounded-pill bg-bg-3"
        role="meter"
        aria-label="Уровень микрофона"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(percent)}
      >
        <div
          className="h-full rounded-pill bg-[linear-gradient(90deg,var(--accent),var(--speak))] transition-[width] duration-[var(--t-fast)] ease-[var(--e-out)]"
          style={{ width: `${percent}%` }}
        />
      </div>
      <p className="text-sm text-text-2">
        Метр оживает, пока работает голосовой движок. Говорите обычным голосом: полоса должна
        уверенно двигаться, но не упираться в правый край.
      </p>
    </div>
  );
}
