import { useMemo } from 'react';
import { Button } from '../components/ui';
import { useGulStore } from '../state/store';
import type { ScreenTrack } from './controller';
import { MediaTile } from './MediaTile';
import { useScreenShare } from './ScreenShareContext';

/** Idle viewing reserves no chat space; the capture toggle lives in the bottom bar. */
export function ScreenSharePanel() {
  const screenShare = useScreenShare();
  const deafened = useGulStore((state) => state.deafened);
  if (!screenShare) return null;
  if (screenShare.openBrowser) return (
    <section aria-label="Демонстрации экрана" data-testid="screen-share-panel" className="shrink-0 border-b border-line-soft bg-bg-1 px-4 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <p className="text-xs text-text-3">Демонстрации открываются в браузере. Голос остаётся в Gul.</p>
        <Button variant="quiet" disabled={screenShare.openingBrowser} onClick={() => void screenShare.openBrowser?.()}>Открыть демонстрации</Button>
      </div>
      {screenShare.browserError && <p role="alert" className="text-xs text-danger">{screenShare.browserError}</p>}
    </section>
  );
  const { controller, snapshot } = screenShare;
  const videos = snapshot.tracks.filter((track) => track.kind === 'video');
  if (!videos.length && !snapshot.warning && !snapshot.error) return null;
  const note = [
    snapshot.sharing ? (snapshot.screenAudio ? 'Вы показываете экран со звуком.' : 'Вы показываете экран без звука.') : '',
    deafened && videos.some((video) => !video.local) ? 'Звук демонстраций выключен.' : '',
  ].filter(Boolean).join(' ');

  return (
    <section aria-label="Демонстрации экрана" data-testid="screen-share-panel"
      className="shrink-0 border-b border-line-soft bg-bg-1 px-4 py-3">
      {note && <p className="mb-2 text-xs text-text-3" role="status" data-testid="screen-share-status">{note}</p>}
      {snapshot.warning && <p role="status" className="mb-2 text-xs text-warning">{snapshot.warning}</p>}
      {snapshot.error && <div className="flex flex-wrap items-center gap-2">
        <p role="alert" className="text-xs text-danger">{snapshot.error}</p>
        {snapshot.status === 'disconnected' && <Button variant="quiet" onClick={() => void controller.join('')}>Повторить</Button>}
      </div>}
      {videos.length > 0 && <div className="grid max-h-[42vh] gap-3 overflow-y-auto">
        {videos.map((item) => <ScreenVideo key={item.id} item={item} />)}
      </div>}
    </section>
  );
}

function ScreenVideo({ item }: { item: ScreenTrack }) {
  const labelled = useMemo(() => ({ ...item, participant: item.local ? 'Вы' : (item.displayName || item.participant) }), [item]);
  return <MediaTile item={labelled} />;
}
