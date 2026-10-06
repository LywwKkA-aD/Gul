import { useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from 'react';
import { ScreenShareService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { LiveKitController } from './controller';
import { ScreenShareContext } from './ScreenShareContext';
import { sessionGrantProvider, type ScreenSession } from './session';

/** Keyed by authenticated epoch/channel; replacing the scope ends capture. */
export function ScreenShareProvider({ session, children }: { session: ScreenSession; children: ReactNode }) {
  const [controller] = useState(() => new LiveKitController(
    sessionGrantProvider(session, (epoch, channelId) => ScreenShareService.Grant(epoch, channelId)),
    { subscribeAudio: false, allowServerIce: true, stopSharingOnReconnect: true },
  ));
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const canCapture = typeof navigator.mediaDevices?.getDisplayMedia === 'function';
  const value = useMemo(() => ({ controller, snapshot, canCapture }), [controller, snapshot, canCapture]);

  useEffect(() => {
    void controller.join('');
    const leave = () => { void controller.leave(); };
    window.addEventListener('pagehide', leave);
    return () => {
      window.removeEventListener('pagehide', leave);
      leave();
    };
  }, [controller]);

  return <ScreenShareContext value={value}>{children}</ScreenShareContext>;
}
