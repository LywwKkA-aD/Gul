import { useEffect, useRef, useState } from 'react';
import type { AppInfo, CaptureCapabilities } from '../shared/contracts.ts';
import type { MediaController } from './media/controller.ts';
import { VoiceSettingsPanel } from './VoiceSettingsPanel.tsx';
import { Dialog } from './Dialog.tsx';
import { shortcutFromKey, type Preferences } from './preferences.ts';
import { audioDeviceOptions } from './audio-device-options.ts';

export function SettingsDialog({
  preferences,
  media,
  capabilities,
  appInfo,
  onChange,
  onClose,
}: {
  preferences: Preferences;
  media: MediaController;
  capabilities: CaptureCapabilities | null;
  appInfo: AppInfo | null;
  onChange: (patch: Partial<Preferences>) => Promise<void>;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<'sound' | 'keys' | 'about'>('sound');
  const [devices, setDevices] = useState<readonly MediaDeviceInfo[]>([]);
  const [busy, setBusy] = useState(false);
  const [recording, setRecording] = useState(false);
  const [diagnosticSaved, setDiagnosticSaved] = useState(false);
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
        <button
          role="tab"
          aria-selected={tab === 'about'}
          aria-controls="about-settings"
          id="about-tab"
          onClick={() => {
            setTab('about');
            setRecording(false);
          }}
        >
          О приложении
        </button>
      </div>
      {tab === 'sound' ? (
        <section role="tabpanel" id="sound-settings" aria-labelledby="sound-tab">
          {(['audioinput', 'audiooutput'] as const).map((kind) => {
            const available = audioDeviceOptions(devices, kind);
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
          <VoiceSettingsPanel
            media={media}
            busy={busy}
            onChange={(patch) => change({ voice: { ...preferences.voice, ...patch } })}
          />
          <label className="checkbox-control">
            <input
              type="checkbox"
              checked={preferences.soundNotifications}
              disabled={busy}
              onChange={(event) => void change({ soundNotifications: event.target.checked })}
            />
            Звуки действий
          </label>
          <p className="subtle settings-help">{capabilities?.details}</p>
          <p className="subtle settings-help">
            Звук демонстрации передаётся отдельно от микрофона. Захват системного звука зависит от источника и
            операционной системы.
          </p>
        </section>
      ) : tab === 'keys' ? (
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
            Режим клавиши
            <select
              aria-label="Режим клавиши микрофона"
              value={preferences.hotkeyMode}
              disabled={busy || recording}
              onChange={(event) =>
                void change({ hotkeyMode: event.target.value as Preferences['hotkeyMode'] })
              }
            >
              <option value="toggle">Нажать — включить / выключить</option>
              <option value="hold">Удерживать — говорить</option>
            </select>
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
            {preferences.hotkeyMode === 'hold'
              ? 'Говорите, удерживая клавишу. На Linux система может попросить разрешить глобальное сочетание. Этот режим доступен на Windows и в окружениях Linux с поддержкой глобальных клавиш.'
              : 'Одно нажатие включает передачу голоса, следующее выключает. Работает и во время игры.'}{' '}
            После смены канала микрофон выключен.
          </p>
        </section>
      ) : (
        <section role="tabpanel" id="about-settings" aria-labelledby="about-tab">
          <p>Gul {appInfo?.version ?? ''}</p>
          {appInfo?.update && (
            <button
              className="secondary-button"
              onClick={() =>
                void window.gul.openUpdate().catch(() => setError('Не удалось открыть страницу обновления.'))
              }
            >
              Скачать {appInfo.update.version}
            </button>
          )}
          <p className="subtle settings-help">
            Для диагностики можно сохранить архив со сведениями о версии и событиях подключения. Пароли,
            адреса серверов, переписка и звук в него не попадают.
          </p>
          <button
            className="secondary-button"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError('');
              try {
                setDiagnosticSaved(await window.gul.diagnostics());
              } catch {
                setError('Не удалось сохранить диагностику.');
              } finally {
                if (mounted.current) setBusy(false);
              }
            }}
          >
            Сохранить диагностику
          </button>
          {diagnosticSaved && (
            <p role="status" className="subtle">
              Архив сохранён.
            </p>
          )}
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
