import type { LiveKitController, LiveKitSnapshot } from './controller';

type ShareState = Pick<LiveKitSnapshot, 'status' | 'sharing' | 'pendingShare'>;

export function screenShareControl(snapshot: ShareState | null, canCapture: boolean) {
  const active = Boolean(snapshot?.sharing || snapshot?.pendingShare);
  const label = snapshot?.pendingShare ? 'Отменить выбор экрана'
    : snapshot?.sharing ? 'Остановить показ'
      : !snapshot || snapshot.status === 'connecting' ? 'Подключение демонстраций…'
        : snapshot.status === 'reconnecting' ? 'Демонстрации переподключаются…'
          : snapshot.status === 'disconnected' ? 'Демонстрации не подключены'
            : !canCapture ? 'Захват экрана недоступен в этой оболочке' : 'Показать экран';
  return { label, active, disabled: !active && (!canCapture || snapshot?.status !== 'connected') };
}

/** Read at click time and call capture before yielding the browser user gesture. */
export function toggleScreenShare(controller: Pick<LiveKitController, 'getSnapshot' | 'share' | 'stopShare'>): Promise<void> {
  const { sharing, pendingShare } = controller.getSnapshot();
  return sharing || pendingShare ? controller.stopShare() : controller.share();
}
