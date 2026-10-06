import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { ScreenShareService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { LiveKitController } from './controller';
import { ScreenShareContext } from './ScreenShareContext';
import { sessionGrantProvider, type ScreenSession } from './session';
import { runtimeIssue } from './connection';

/** Keyed by authenticated epoch/channel; replacing the scope ends capture. */
export function ScreenShareProvider({ session, children }: { session: ScreenSession; children: ReactNode }) {
  const [controller] = useState(() => new LiveKitController(
    sessionGrantProvider(session, (epoch, channelId) => ScreenShareService.Grant(epoch, channelId)),
    { subscribeAudio: false, allowServerIce: true, stopSharingOnReconnect: true, retryJoin: true },
  ));
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const canCapture = typeof navigator.mediaDevices?.getDisplayMedia === 'function';
  const external = Boolean(runtimeIssue()) || !canCapture;
  const [openingBrowser, setOpeningBrowser] = useState(false);
  const [browserError, setBrowserError] = useState('');
  const active = useRef(false);
  const opening = useRef(false);
  const openBrowser = useCallback(async () => {
    if (!active.current || opening.current) return;
    opening.current = true;
    setOpeningBrowser(true);
    setBrowserError('');
    try { await ScreenShareService.OpenBrowser(session.epoch, session.channelId); }
    catch { if (active.current) setBrowserError('Не удалось открыть браузер. Проверьте браузер по умолчанию и повторите попытку.'); }
    finally {
      opening.current = false;
      if (active.current) setOpeningBrowser(false);
    }
  }, [session.epoch, session.channelId]);
  const value = useMemo(() => ({ controller, snapshot, canCapture,
    ...(external ? { openBrowser, openingBrowser, browserError } : {}),
  }), [controller, snapshot, canCapture, external, openBrowser, openingBrowser, browserError]);

  useEffect(() => {
    active.current = true;
    if (!external) void controller.join('');
    const leave = () => { void controller.leave(); };
    window.addEventListener('pagehide', leave);
    return () => {
      active.current = false;
      window.removeEventListener('pagehide', leave);
      leave();
    };
  }, [controller, external]);

  return <ScreenShareContext value={value}>{children}</ScreenShareContext>;
}
