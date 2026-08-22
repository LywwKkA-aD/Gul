import { useEffect, useRef } from 'react';
import { AudioService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from './store';

// Push-to-talk while the window has focus. A key pressed with the app in the
// background belongs to whatever is in front, so nothing here reaches for the
// OS: the global shortcut is M4 (PLAN.md 7).

/** True for events that belong to something the user is typing into. */
function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
}

/** Drives AudioService.SetPTT from the configured key while the gate runs in
    push-to-talk mode. Mount once, at the application root. */
export function usePushToTalk(): void {
  const active = useGulStore((s) => s.gateMode === 'ptt' && !s.pttCapturing);
  const pttKey = useGulStore((s) => s.pttKey);

  // What the engine was last told. The store flag is an indicator and is
  // cleared on disconnect; the release still has to reach the engine, or a
  // key held across a reconnect would leave the gate open.
  const heldRef = useRef(false);

  useEffect(() => {
    const press = (held: boolean) => {
      if (heldRef.current === held) return;
      heldRef.current = held;
      useGulStore.getState().setPttHeld(held);
      AudioService.SetPTT(held).catch(console.error);
    };

    if (!active) {
      press(false);
      return;
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.code !== pttKey || e.repeat || isTypingTarget(e.target)) return;
      // Only the bound key is ours to swallow, and only outside text fields.
      // Without this, Space would also press whatever button holds focus.
      e.preventDefault();
      press(true);
    };

    const onKeyUp = (e: KeyboardEvent) => {
      if (e.code !== pttKey) return;
      if (heldRef.current) e.preventDefault();
      press(false);
    };

    // A key released while the window is in the background never reports its
    // keyup here, so the gate would stay open until the next press.
    const onBlur = () => press(false);

    window.addEventListener('keydown', onKeyDown);
    window.addEventListener('keyup', onKeyUp);
    window.addEventListener('blur', onBlur);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('keyup', onKeyUp);
      window.removeEventListener('blur', onBlur);
      press(false);
    };
  }, [active, pttKey]);
}
