import { useState } from 'react';
import { Dialog } from './Dialog.tsx';
import { screenPresets, parseScreenQuality, type ScreenQuality } from './media/screen-settings.ts';

export function ShareStartDialog({
  quality,
  onStart,
  onClose,
}: {
  quality: ScreenQuality;
  onStart: (quality: ScreenQuality) => void;
  onClose: () => void;
}) {
  const [selected, setSelected] = useState(quality);
  return (
    <Dialog title="Начать демонстрацию" onClose={onClose}>
      <h2>Начать демонстрацию</h2>
      <label>
        Качество демонстрации
        <select
          aria-label="Качество демонстрации"
          value={selected}
          onChange={(event) => setSelected(parseScreenQuality(event.target.value))}
        >
          {screenPresets.map((preset) => (
            <option key={preset.id} value={preset.id}>
              {preset.label}
            </option>
          ))}
        </select>
      </label>
      <p className="subtle">
        Затем выберите экран или окно. Звук компьютера передаётся автоматически, если доступен.
      </p>
      <div className="share-start-actions">
        <button className="secondary-button" onClick={onClose}>
          Отмена
        </button>
        <button className="primary" onClick={() => onStart(selected)}>
          Выбрать экран
        </button>
      </div>
    </Dialog>
  );
}
