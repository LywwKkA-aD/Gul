import React, { useSyncExternalStore } from 'react';
import { createRoot } from 'react-dom/client';
import { ScreenSharePanel } from '../../src/livekit/ScreenSharePanel';
import { ScreenShareProvider } from '../../src/livekit/ScreenShareProvider';
import { BottomBar } from '../../src/components/BottomBar';
import { createTestPattern } from '../../src/livekit/pattern';
import { screenSession } from '../../src/livekit/session';
import { useGulStore } from '../../src/state/store';
import type { ConnectionStatus } from '../../src/state/types';
import '../../src/style.css';

declare global {
  interface Window {
    gulPanelTest: {
      status(next: Partial<ConnectionStatus>): void;
      deafen(): void;
      deferCapture(): void;
      releaseCapture(): void;
      captureStates(): MediaStreamTrackState[][];
      cleanup(): void;
    };
    gulPeerCount: number;
    gulPeerIceServers: RTCIceServer[][];
    gulPanelGrantReplies: number;
  }
}

const captures: MediaStream[] = [];
const cleanup: (() => void)[] = [];
let pendingCapture: (() => void) | undefined;
let deferCapture = false;

// Synthetic display media avoids OS permissions; the panel still invokes the actual
// SDK capture/publication path and the receiver decodes video from the local SFU.
navigator.mediaDevices.getDisplayMedia = async () => {
  if (deferCapture) await new Promise<void>((resolve) => { pendingCapture = resolve; });
  const pattern = await createTestPattern();
  cleanup.push(() => pattern.cleanup?.());
  const stream = new MediaStream(pattern.tracks.map((track) => track.mediaStreamTrack));
  captures.push(stream);
  return stream;
};

const status: ConnectionStatus = { state: 'connected', server: 'http://127.0.0.1:8787', selfChannel: 1, epoch: 1 };
useGulStore.getState().setStatus(status);

Object.assign(window, {
  gulPanelTest: {
    status(next: Partial<ConnectionStatus>) { useGulStore.getState().setStatus({ ...status, ...next }); },
    deafen() { useGulStore.getState().setVoiceGates(true, true); },
    deferCapture() { deferCapture = true; },
    releaseCapture() { deferCapture = false; pendingCapture?.(); pendingCapture = undefined; },
    captureStates() { return captures.map((stream) => stream.getTracks().map((track) => track.readyState)); },
    cleanup() { cleanup.forEach((release) => release()); },
  },
});

function Harness() {
  const snapshot = useSyncExternalStore(useGulStore.subscribe, useGulStore.getState);
  const session = screenSession(snapshot.status);
  return <div className="mx-auto max-w-3xl p-5">{session
    ? <ScreenShareProvider key={session.key} session={session}>
      <div className="mb-4 w-[240px]"><BottomBar /></div>
      <ScreenSharePanel />
    </ScreenShareProvider>
    : <p data-testid="no-screen-session">Сессия завершена</p>}</div>;
}

createRoot(document.getElementById('root')!).render(<React.StrictMode><Harness /></React.StrictMode>);
