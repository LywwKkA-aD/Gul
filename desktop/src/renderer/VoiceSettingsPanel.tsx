import { useSyncExternalStore } from 'react';
import type { MediaController } from './media/controller.ts';
import type { VoiceSettings } from './media/voice-gate.ts';

export function VoiceSettingsPanel({
  media,
  onChange,
  busy,
}: {
  media: MediaController;
  onChange: (patch: Partial<VoiceSettings>) => Promise<void>;
  busy: boolean;
}) {
  const snapshot = useSyncExternalStore(media.subscribe, media.getSnapshot);
  const voice = snapshot.voiceSettings;
  return (
    <div className="voice-settings">
      <label>
        Режим микрофона
        <select
          aria-label="Режим микрофона"
          value={voice.mode}
          disabled={busy}
          onChange={(event) => void onChange({ mode: event.target.value as VoiceSettings['mode'] })}
        >
          <option value="continuous">Открытый микрофон</option>
          <option value="vad">Активация голосом</option>
        </select>
      </label>
      <label>
        Уровень микрофона
        <meter aria-label="Уровень микрофона" min={0} max={1} value={snapshot.micLevel} />
      </label>
      <label>
        Усиление микрофона · {Math.round(voice.inputGain * 100)}%
        <input
          aria-label="Усиление микрофона"
          type="range"
          min={0}
          max={2}
          step={0.05}
          value={voice.inputGain}
          disabled={busy}
          onChange={(event) => void onChange({ inputGain: Number(event.target.value) })}
        />
      </label>
      {voice.mode === 'vad' && (
        <>
          <label>
            Порог активации · {voice.thresholdDb} дБ
            <input
              aria-label="Порог активации"
              type="range"
              min={-80}
              max={-6}
              value={voice.thresholdDb}
              disabled={busy}
              onChange={(event) => void onChange({ thresholdDb: Number(event.target.value) })}
            />
          </label>
          <label>
            Задержка выключения · {voice.holdMs} мс
            <input
              aria-label="Задержка выключения"
              type="range"
              min={0}
              max={1000}
              step={50}
              value={voice.holdMs}
              disabled={busy}
              onChange={(event) => void onChange({ holdMs: Number(event.target.value) })}
            />
          </label>
        </>
      )}
      {(
        [
          ['echoCancellation', 'Убирать эхо'],
          ['noiseSuppression', 'Шумоподавление'],
          ['autoGainControl', 'Автоматическая громкость'],
        ] as const
      ).map(([key, label]) => (
        <label className="checkbox-control" key={key}>
          <input
            type="checkbox"
            checked={voice[key]}
            disabled={busy}
            onChange={(event) => void onChange({ [key]: event.target.checked })}
          />
          {label}
        </label>
      ))}
      {!snapshot.voiceProcessingAvailable && snapshot.state !== 'disconnected' && (
        <p className="subtle">Измерение уровня и активация голосом сейчас недоступны.</p>
      )}
    </div>
  );
}
