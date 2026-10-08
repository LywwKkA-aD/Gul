import { useSyncExternalStore } from 'react';
import type { MediaController } from './media/controller.ts';
import type { VoiceSettings } from './media/voice-gate.ts';
import { SettingsRange } from './SettingsRange.tsx';
import type { RangePatch } from './range-updates.ts';

export function VoiceSettingsPanel({
  media,
  onChange,
  onAdjust,
  busy,
}: {
  media: MediaController;
  onChange: (patch: Partial<VoiceSettings>) => Promise<void>;
  onAdjust: (patch: RangePatch) => Promise<void>;
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
        <SettingsRange
          aria-label="Усиление микрофона"
          min={0}
          max={2}
          step={0.05}
          value={voice.inputGain}
          disabled={busy}
          onChange={(value) => onAdjust({ inputGain: value })}
        />
      </label>
      {voice.mode === 'vad' && (
        <>
          <label>
            Порог активации · {voice.thresholdDb} дБ
            <SettingsRange
              aria-label="Порог активации"
              min={-80}
              max={-6}
              value={voice.thresholdDb}
              disabled={busy}
              onChange={(value) => onAdjust({ thresholdDb: value })}
            />
          </label>
          <label>
            Задержка выключения · {voice.holdMs} мс
            <SettingsRange
              aria-label="Задержка выключения"
              min={0}
              max={1000}
              step={50}
              value={voice.holdMs}
              disabled={busy}
              onChange={(value) => onAdjust({ holdMs: value })}
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
