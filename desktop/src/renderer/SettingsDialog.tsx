import { useEffect, useRef, useState } from 'react';
import { Dialog } from './Dialog.tsx';
import { shortcutFromKey, type Preferences } from './preferences.ts';

export function SettingsDialog({
  preferences,
  onChange,
  onClose,
}: {
  preferences: Preferences;
  onChange: (patch: Partial<Preferences>) => Promise<void>;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<'sound' | 'keys'>('sound');
  const [devices, setDevices] = useState<readonly MediaDeviceInfo[]>([]);
  const [busy, setBusy] = useState(false);
  const [recording, setRecording] = useState(false);
  const [error, setError] = useState('');
  const shortcutInput = useRef<HTMLInputElement>(null);
  const mounted = useRef(true);
  const refreshDevices = async () => {
    try {
      const next = await navigator.mediaDevices.enumerateDevices();
      if (mounted.current) {
        setDevices(next);
        setError('');
      }
    } catch {
      if (mounted.current) setError('Не удалось получить список аудиоустройств.');
    }
  };
  useEffect(() => {
    mounted.current = true;
    void refreshDevices();
    const update = () => void refreshDevices();
    navigator.mediaDevices.addEventListener('devicechange', update);
    return () => {
      mounted.current = false;
      navigator.mediaDevices.removeEventListener('devicechange', update);
    };
  }, []);
  const change = async (patch: Partial<Preferences>) => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      await onChange(patch);
    } catch (failure) {
      if (mounted.current)
        setError(failure instanceof Error ? failure.message : 'Не удалось применить настройку.');
    } finally {
      if (mounted.current) setBusy(false);
    }
  };
  return (
    <Dialog title="Настройки" className="settings-dialog" onClose={onClose}>
      <h2>Настройки</h2>
      <div className="settings-tabs" role="tablist" aria-label="Раздел настроек">
        <button
          role="tab"
          aria-selected={tab === 'sound'}
          aria-controls="sound-settings"
          id="sound-tab"
          onClick={() => {
            setTab('sound');
            setRecording(false);
          }}
        >
          Звук
        </button>
        <button
          role="tab"
          aria-selected={tab === 'keys'}
          aria-controls="keys-settings"
          id="keys-tab"
          onClick={() => setTab('keys')}
        >
          Клавиши
        </button>
      </div>
      {tab === 'sound' ? (
        <section role="tabpanel" id="sound-settings" aria-labelledby="sound-tab">
          {(['audioinput', 'audiooutput'] as const).map((kind) => {
            const available = devices.filter(
              (device) => device.kind === kind && device.deviceId !== 'default',
            );
            const savedUnavailable =
              preferences[kind] !== 'default' &&
              !available.some((device) => device.deviceId === preferences[kind]);
            return (
              <label key={kind}>
                {kind === 'audioinput' ? 'Микрофон' : 'Наушники или динамики'}
                <select
                  aria-label={kind === 'audioinput' ? 'Устройство микрофона' : 'Устройство вывода'}
                  value={preferences[kind]}
                  disabled={busy}
                  onChange={(event) => void change({ [kind]: event.target.value })}
                >
                  <option value="default">По умолчанию</option>
                  {savedUnavailable && (
                    <option value={preferences[kind]}>Сохранённое устройство недоступно</option>
                  )}
                  {available.map((device, index) => (
                    <option key={device.deviceId} value={device.deviceId}>
                      {device.label || `Аудиоустройство ${index + 1}`}
                    </option>
                  ))}
                </select>
              </label>
            );
          })}
          <button className="secondary-button" onClick={() => void refreshDevices()} disabled={busy}>
            Обновить устройства
          </button>
          <p className="subtle settings-help">
            Звук демонстрации передаётся отдельно от микрофона. Захват системного звука зависит от источника и
            операционной системы.
          </p>
        </section>
      ) : (
        <section role="tabpanel" id="keys-settings" aria-labelledby="keys-tab">
          <label className="checkbox-control">
            <input
              type="checkbox"
              aria-label="Глобальная клавиша микрофона"
              checked={preferences.toggleEnabled}
              disabled={busy || recording}
              onChange={(event) => void change({ toggleEnabled: event.target.checked })}
            />
            Глобальная клавиша микрофона
          </label>
          <label>
            Сочетание клавиш
            <div className="shortcut-picker">
              <input
                ref={shortcutInput}
                aria-label="Сочетание клавиш микрофона"
                readOnly
                value={recording ? 'Нажмите сочетание…' : preferences.shortcut}
                onKeyDown={(event) => {
                  if (!recording) return;
                  event.preventDefault();
                  event.stopPropagation();
                  if (event.key === 'Escape') {
                    setRecording(false);
                    return;
                  }
                  const shortcut = shortcutFromKey(event.nativeEvent);
                  if (!shortcut) return;
                  setRecording(false);
                  void change({ shortcut });
                }}
              />
              <button
                className="secondary-button"
                disabled={busy}
                onClick={() => {
                  setRecording(!recording);
                  shortcutInput.current?.focus();
                }}
              >
                {recording ? 'Отмена' : 'Изменить'}
              </button>
            </div>
          </label>
          <p className="subtle settings-help">
            Одно нажатие включает передачу голоса, следующее выключает. Работает и во время игры. После смены
            канала микрофон выключен; включите его этой клавишей или кнопкой.
          </p>
        </section>
      )}
      {error && (
        <p className="dialog-error error" role="alert">
          {error}
        </p>
      )}
    </Dialog>
  );
}
