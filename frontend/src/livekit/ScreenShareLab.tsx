import { useEffect, useState, useSyncExternalStore } from 'react';
import { MonitorIcon } from '@phosphor-icons/react/dist/csr/Monitor';
import { Button, Field, TextInput } from '../components/ui';
import { LiveKitController } from './controller';
import { localGrant } from './grant';
import { MediaTile } from './MediaTile';
import { createTestPattern } from './pattern';

const labels = {
  disconnected: 'Не подключено', connecting: 'Подключение…',
  connected: 'Подключено', reconnecting: 'Переподключение…',
};

export default function ScreenShareLab() {
  const [controller] = useState(() => new LiveKitController(localGrant));
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [identity, setIdentity] = useState(() => `local-${Array.from(crypto.getRandomValues(new Uint8Array(4)), (n) => n.toString(16).padStart(2, '0')).join('')}`);
  const connected = snapshot.status === 'connected';
  const idle = snapshot.status === 'disconnected';
  const canCapture = typeof navigator.mediaDevices?.getDisplayMedia === 'function';
  const videos = snapshot.tracks.filter((track) => track.kind === 'video');

  useEffect(() => {
    const leave = () => { void controller.leave(); };
    window.addEventListener('pagehide', leave);
    return () => { window.removeEventListener('pagehide', leave); leave(); };
  }, [controller]);

  return (
    <main className="h-dvh overflow-y-auto bg-bg-0 px-6 py-7 text-ui text-text-1">
      <div className="mx-auto flex max-w-5xl flex-col gap-5">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <div className="rounded-lg bg-sb-1 p-3 text-sb-text-1"><MonitorIcon size={24} /></div>
            <div>
              <h1 className="font-display text-lg font-medium">Gul · демонстрация экрана</h1>
              <p className="mt-1 text-sm text-text-2">Локальная тестовая комната · LiveKit</p>
            </div>
          </div>
          <span data-testid="lab-status" role="status" className={`rounded-pill px-3 py-1.5 text-sm ${connected ? 'bg-[var(--accent-weak)] text-[var(--accent-text)]' : 'bg-bg-4 text-text-2'}`}>
            {labels[snapshot.status]}
          </span>
        </header>

        <section aria-label="Управление демонстрацией" className="rounded-lg bg-bg-1 p-5 shadow-[var(--sh-sm)]">
          <div className="flex flex-wrap items-end gap-3">
            <Field label="Имя участника" className="w-56">
              <TextInput value={identity} disabled={!idle} maxLength={40}
                onChange={(event) => setIdentity(event.target.value)} />
            </Field>
            {idle ? <Button size="lg" disabled={!identity.trim()} onClick={() => void controller.join(identity.trim())}>Войти в тестовую комнату</Button>
              : <Button size="lg" variant="quiet" onClick={() => void controller.leave()}>Выйти</Button>}
            <span className="pb-2 text-sm text-text-3">Участников: {snapshot.participants.length}</span>
          </div>
          <div className="mt-5 flex flex-wrap gap-3 border-t border-line-soft pt-4">
            {snapshot.sharing || snapshot.pendingShare
              ? <Button onClick={() => void controller.stopShare()}>Остановить показ</Button>
              : <Button disabled={!connected || !canCapture} onClick={() => void controller.share()}>Показать экран со звуком</Button>}
            <Button variant="quiet" disabled={!connected || snapshot.sharing || snapshot.pendingShare}
              onClick={() => void controller.shareWith(createTestPattern)}>Тест: картинка и тон</Button>
            <Button variant="quiet" disabled={!connected} onClick={() => void controller.startAudio()}>Включить звук</Button>
          </div>
          <p className="mt-3 text-sm text-text-2">
            {snapshot.sharing ? (snapshot.screenAudio ? 'Передаются видео и звук.' : 'Передаётся видео. Звук не захвачен.')
              : 'После входа выберите окно, экран или вкладку. В диалоге выбора включите передачу звука, если она доступна.'}
          </p>
          {!canCapture && <p className="mt-2 text-sm text-warning">В этой оболочке захват экрана недоступен. Просмотр и тестовая картинка доступны; захват можно проверить в Chrome по адресу http://127.0.0.1:8787/#livekit.</p>}
          {snapshot.warning && <p role="status" className="mt-2 text-sm text-warning">{snapshot.warning}</p>}
          {snapshot.error && <p role="alert" className="mt-2 text-sm text-danger">{snapshot.error}</p>}
        </section>

        <section aria-label="Демонстрации" className="grid gap-4">
          {videos.length === 0 && <div className="flex min-h-56 flex-col items-center justify-center rounded-lg border border-dashed border-line bg-bg-3 p-8 text-center">
            <MonitorIcon size={36} className="mb-4 text-text-3" />
            <h2 className="font-medium">Здесь появится демонстрация</h2>
            <p className="mt-2 max-w-lg text-sm text-text-2">Откройте эту комнату во втором окне с другим именем. Один участник показывает экран, другой смотрит и слушает.</p>
          </div>}
          {snapshot.tracks.map((track) => <MediaTile key={track.id} item={track} />)}
        </section>

        <footer className="space-y-2 pb-4 text-sm text-text-3">
          <p>Микрофон и камера не включаются. Кнопка теста передаёт движущуюся картинку и тихий тон 440 Гц; она проверяет доставку видео и аудио.</p>
          <p>При захвате всего системного звука собеседники могут услышать себя. Для первого теста используйте отдельную вкладку со звуком.</p>
          <p>Стенд доступен только на этом компьютере. Рабочий голосовой сервер не используется.</p>
        </footer>
      </div>
    </main>
  );
}
