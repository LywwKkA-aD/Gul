import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { MonitorIcon } from '@phosphor-icons/react/dist/csr/Monitor';
import { Button } from '../components/ui';
import { LiveKitController } from './controller';
import { ScreenShareContext } from './ScreenShareContext';
import { ScreenSharePanel } from './ScreenSharePanel';
import { screenShareControl, toggleScreenShare } from './screenShareControl';
import type { BrowserScreenSession } from './browserSession';

export function BrowserScreen({ session }: { session: BrowserScreenSession }) {
  const [controller] = useState(() => new LiveKitController(session.grant, {
    subscribeAudio: false, allowServerIce: true, stopSharingOnReconnect: true, retryJoin: true,
  }));
  const [ended, setEnded] = useState(false);
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const canCapture = typeof navigator.mediaDevices?.getDisplayMedia === 'function';
  const value = useMemo(() => ({ controller, snapshot, canCapture }), [controller, snapshot, canCapture]);
  const control = screenShareControl(snapshot, canCapture);
  useEffect(() => {
    if (ended) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    void controller.join('');
    const check = async () => {
      try { await session.check(); }
      catch { if (active) setEnded(true); return; }
      if (active) timer = setTimeout(() => void check(), 1000);
    };
    void check();
    const leave = () => {
      active = false;
      clearTimeout(timer);
      void controller.leave();
      void session.close();
    };
    window.addEventListener('pagehide', leave);
    return () => { window.removeEventListener('pagehide', leave); leave(); };
  }, [controller, session, ended]);

  return <main className="mx-auto min-h-screen max-w-5xl bg-bg-0 px-4 py-6 text-text-1">
    <header className="mb-5 flex flex-wrap items-center justify-between gap-3">
      <h1 className="flex items-center gap-2 font-display text-lg"><MonitorIcon size={22} />Демонстрации · Gul</h1>
      {!ended && <div className="flex gap-2">
        <Button disabled={control.disabled} onClick={() => void toggleScreenShare(controller)} aria-pressed={control.active}>{control.label}</Button>
        <Button variant="quiet" onClick={() => setEnded(true)}>Закрыть демонстрации</Button>
      </div>}
    </header>
    {ended ? <p role="status">Сессия завершена. Откройте демонстрации снова из Gul.</p> : <>
      <p className="mb-4 text-sm text-text-3">Оставьте Gul подключённым к каналу. Звук демонстраций и управление громкостью остаются в приложении.</p>
      <ScreenShareContext value={value}><ScreenSharePanel /></ScreenShareContext>
      {!snapshot.tracks.length && !snapshot.error && <p className="py-8 text-center text-sm text-text-3" role="status">
        {snapshot.status === 'connected' ? 'Демонстраций пока нет. Выберите экран, когда будете готовы.' : 'Подключаем демонстрации…'}
      </p>}
    </>}
  </main>;
}
