import { useEffect, useState } from 'react';
import { Events, Window } from '@wailsio/runtime';
import { IS_MAC, markFullscreen } from './platform';

// Whether the window is in fullscreen. Only macOS cares: there the traffic
// lights float over our first --titlebar-h pixels and we keep a strip empty
// for them - but fullscreen hides the buttons entirely, so the strip would be
// nothing but dead, undraggable chrome.
//
// Wails posts the AppKit notifications straight through to the frontend
// (pkg/application/webview_window_darwin.m -> dispatchWindowEvent, which
// re-emits them under the names in pkg/events/events.go); the current value is
// read once, because a window can start fullscreen.

const ENTER = 'mac:WindowDidEnterFullScreen';
const EXIT = 'mac:WindowDidExitFullScreen';

/** True while the window is fullscreen. Always false off macOS, where the
 *  strip does not exist in the first place. */
export function useFullscreen(): boolean {
  const [fullscreen, setFullscreen] = useState(false);

  useEffect(() => {
    if (!IS_MAC) return;
    let live = true;
    // A window restored into fullscreen never posts the enter notification.
    Window.IsFullscreen()
      .then((now) => {
        if (live) setFullscreen(now === true);
      })
      .catch((e: unknown) => console.error('fullscreen:', e));

    const offEnter = Events.On(ENTER, () => setFullscreen(true));
    const offExit = Events.On(EXIT, () => setFullscreen(false));
    return () => {
      live = false;
      offEnter();
      offExit();
    };
  }, []);

  // The token follows the state, so layout, the modal's top padding and the
  // tooltip's idea of the usable window all move together (styles/tokens.css).
  useEffect(() => {
    markFullscreen(fullscreen);
  }, [fullscreen]);

  return fullscreen;
}
