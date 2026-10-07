import { useEffect, useRef, useState } from 'react';
import type { Snapshot } from './media/model.ts';
import { Icon, Video } from './MediaElements.tsx';
import type { LocalAudioPreference } from './ParticipantControls.tsx';

export function ScreenPanel({
  snapshot,
  localAudio,
  onWatch,
  onVolume,
  onError,
}: {
  snapshot: Snapshot;
  localAudio: Readonly<Record<string, LocalAudioPreference>>;
  onWatch: (identity: string | null) => Promise<void>;
  onVolume: (identity: string, gain: number) => void;
  onError: (message: string) => void;
}) {
  const viewer = useRef<HTMLDivElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const watching = snapshot.screens.find((screen) => screen.watching && !screen.local);
  const selectedVideo = snapshot.videos.find(
    (video) => !video.local && video.identity === watching?.identity,
  );
  const ownVideo = snapshot.videos.find((video) => video.local);
  useEffect(() => {
    const update = () =>
      setFullscreen(Boolean(viewer.current && document.fullscreenElement === viewer.current));
    const exitOnEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || !viewer.current || document.fullscreenElement !== viewer.current) return;
      event.preventDefault();
      void document.exitFullscreen().catch(() => {});
    };
    document.addEventListener('fullscreenchange', update);
    document.addEventListener('keydown', exitOnEscape);
    return () => {
      document.removeEventListener('fullscreenchange', update);
      document.removeEventListener('keydown', exitOnEscape);
    };
  }, []);
  useEffect(() => {
    if (!watching && viewer.current && document.fullscreenElement === viewer.current)
      void document.exitFullscreen().catch(() => {});
  }, [watching?.identity]);
  const watch = async (identity: string | null) => {
    try {
      await onWatch(identity);
    } catch {
      onError('Не удалось открыть демонстрацию. Повторите просмотр.');
    }
  };
  const toggleFullscreen = async () => {
    try {
      if (document.fullscreenElement === viewer.current) await document.exitFullscreen();
      else await viewer.current?.requestFullscreen();
    } catch {
      onError('Не удалось открыть полноэкранный просмотр.');
    }
  };
  if (!snapshot.screens.length) return null;
  return (
    <section className="screens" aria-label="Демонстрации в канале">
      <div className="screen-list">
        {snapshot.screens.map((screen) => (
          <button
            key={screen.identity}
            className={`screen-choice ${screen.watching ? 'selected' : ''}`}
            disabled={screen.local}
            onClick={() => void watch(screen.watching ? null : screen.identity)}
          >
            <Icon name="screen" />
            <span>
              {screen.name}
              <small>{screen.local ? 'Ваш экран' : screen.watching ? 'Смотрите' : 'Смотреть экран'}</small>
            </span>
          </button>
        ))}
      </div>
      {watching && (
        <div className="screen-viewer" ref={viewer}>
          <div className="screen-viewer-heading">
            <strong>{watching.name}</strong>
            <span>Демонстрация</span>
          </div>
          {selectedVideo ? (
            <Video track={selectedVideo.track} local={false} />
          ) : (
            <div className="waiting-screen">Ожидаем изображение…</div>
          )}
          <div className="screen-controls">
            <label className="screen-volume">
              Звук демонстрации
              <input
                aria-label="Звук демонстрации"
                type="range"
                min="0"
                max="2"
                step="0.05"
                value={localAudio[watching.identity]?.gain ?? 1}
                onChange={(event) => onVolume(watching.identity, Number(event.target.value))}
              />
              <output>{Math.round((localAudio[watching.identity]?.gain ?? 1) * 100)}%</output>
            </label>
            <button
              className="icon-button"
              aria-label={fullscreen ? 'Выйти из полноэкранного режима' : 'На весь экран'}
              title={fullscreen ? 'Выйти из полноэкранного режима' : 'На весь экран'}
              onClick={() => void toggleFullscreen()}
            >
              <Icon name={fullscreen ? 'collapse' : 'expand'} />
            </button>
            <button
              className="secondary-button"
              aria-label="Прекратить просмотр"
              onClick={() => void watch(null)}
            >
              Прекратить просмотр
            </button>
          </div>
        </div>
      )}
      {ownVideo && (
        <div className={`own-screen ${watching ? 'compact-preview' : ''}`}>
          <span>Ваш экран</span>
          <Video track={ownVideo.track} local />
        </div>
      )}
    </section>
  );
}
