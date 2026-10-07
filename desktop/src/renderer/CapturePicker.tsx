import { useState } from 'react';
import type { CapturePickerRequest } from '../shared/capture-picker.ts';
import { Dialog } from './Dialog.tsx';
import { Icon } from './MediaElements.tsx';

export function CapturePickerDialog({
  request,
  busy,
  error,
  onSelect,
  onCancel,
}: {
  request: CapturePickerRequest;
  busy: boolean;
  error: string;
  onSelect: (sourceKey: string) => void;
  onCancel: () => void;
}) {
  const [kind, setKind] = useState<'screen' | 'window'>(
    request.sources.some((source) => source.kind === 'screen') ? 'screen' : 'window',
  );
  const [selected, setSelected] = useState<string | null>(null);
  const sources = request.sources.filter((source) => source.kind === kind);
  const chosen = sources.find((source) => source.sourceKey === selected);
  return (
    <Dialog
      title="Демонстрация экрана"
      className="capture-picker-dialog"
      onClose={() => {
        if (!busy) onCancel();
      }}
    >
      <h2>Чем поделиться?</h2>
      <p className="capture-picker-intro">Выберите экран целиком или отдельное окно.</p>
      <div className="settings-tabs capture-picker-tabs" role="tablist" aria-label="Источники демонстрации">
        {(['screen', 'window'] as const).map((category) => (
          <button
            key={category}
            role="tab"
            id={`capture-${category}-tab`}
            aria-selected={kind === category}
            aria-controls="capture-sources"
            disabled={busy}
            onClick={() => {
              setKind(category);
              setSelected(null);
            }}
          >
            {category === 'screen' ? 'Экраны' : 'Окна'}
            <span className="capture-source-count">
              {request.sources.filter((source) => source.kind === category).length}
            </span>
          </button>
        ))}
      </div>
      <section role="tabpanel" id="capture-sources" aria-labelledby={`capture-${kind}-tab`}>
        <fieldset className="capture-source-grid" disabled={busy}>
          <legend className="capture-sr-only">Выберите {kind === 'screen' ? 'экран' : 'окно'}</legend>
          {sources.map((source) => (
            <label key={source.sourceKey} className="capture-source-card">
              <input
                type="radio"
                name="capture-source"
                value={source.sourceKey}
                aria-label={source.name}
                checked={selected === source.sourceKey}
                onChange={() => setSelected(source.sourceKey)}
              />
              <span className="capture-source-body">
                <span className="capture-source-preview">
                  {source.thumbnail ? (
                    <img src={source.thumbnail} alt="" draggable={false} />
                  ) : (
                    <span className="capture-preview-missing">
                      <Icon name="screen" />
                      <span>Предпросмотр недоступен</span>
                    </span>
                  )}
                </span>
                <span className="capture-source-name" title={source.name}>
                  {source.name}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        {!sources.length && (
          <p className="capture-empty">
            {kind === 'screen' ? 'Нет доступных экранов.' : 'Нет доступных окон.'}
          </p>
        )}
      </section>
      <div className="capture-picker-footer">
        <div className="capture-audio-status">
          <span className="capture-audio-label">
            <Icon name={request.audio ? 'voice' : 'volumeOff'} />
            {request.audio ? 'Со звуком компьютера' : 'Без системного звука'}
          </span>
          {request.details && <p className="subtle">{request.details}</p>}
        </div>
        {error && (
          <p className="dialog-error" role="alert">
            {error}
          </p>
        )}
        <div className="capture-picker-actions">
          <span className="capture-selection-name" aria-live="polite">
            {chosen?.name ?? 'Источник не выбран'}
          </span>
          <button className="secondary-button" onClick={onCancel} disabled={busy}>
            Отмена
          </button>
          <button
            className="capture-share-button"
            disabled={!chosen || busy}
            onClick={() => {
              if (chosen && !busy) onSelect(chosen.sourceKey);
            }}
          >
            {busy ? 'Подключаем…' : 'Показать'}
          </button>
        </div>
      </div>
    </Dialog>
  );
}
