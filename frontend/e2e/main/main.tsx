import React from 'react';
import { createRoot } from 'react-dom/client';
import { setTransport } from '@wailsio/runtime';
import { MainScreen } from '../../src/app/MainScreen';
import { markPlatform } from '../../src/app/platform';
import { useGulStore } from '../../src/state/store';
import type { ChannelNode, ConnectionStatus } from '../../src/state/types';
import '../../src/style.css';

declare global {
  interface Window {
    gulMainScreenTest: {
      churn(count: number): Promise<void>;
      status(next: Partial<ConnectionStatus>): void;
      captures(): MediaStreamTrackState[][];
    };
    gulPanelGrantReplies: number;
  }
}

// Only native window calls and the service bridge are fixtures. MainScreen,
// its lazy provider chunk, React, the controller and media transport are real.
setTransport({ call: async () => false });
markPlatform();
const store = useGulStore;
const status: ConnectionStatus = {
  state: 'connected', server: 'http://127.0.0.1:8787', selfSession: 1, selfChannel: 0, epoch: 7,
};
const tree = (revision: number): ChannelNode => ({
  id: 0, name: 'Gul LiveKit', position: 0,
  users: [
    { session: 1, hash: '', key: 'voice.self', name: 'Local tester', channelId: 0, selfMute: false, selfDeaf: false, isSelf: true },
    { session: 2, hash: '', key: 'voice.peer', name: `Peer ${revision}`, channelId: 0, selfMute: false, selfDeaf: false, isSelf: false },
  ],
  children: [{ id: 1, name: 'Games', position: 1, users: [], children: [] }],
});
store.getState().setStatus(status);
store.getState().setTree(tree(0));

const captures: MediaStream[] = [];
navigator.mediaDevices.getDisplayMedia = async () => {
  const canvas = document.createElement('canvas');
  canvas.width = 640;
  canvas.height = 360;
  const context = canvas.getContext('2d')!;
  let frame = 0;
  const draw = () => {
    context.fillStyle = `hsl(${frame++ % 360} 60% 50%)`;
    context.fillRect(0, 0, canvas.width, canvas.height);
  };
  draw();
  const timer = window.setInterval(draw, 30);
  const stream = canvas.captureStream(30);
  stream.getTracks().forEach((track) => {
    const stop = track.stop.bind(track);
    track.stop = () => { clearInterval(timer); stop(); };
  });
  captures.push(stream);
  return stream;
};

window.gulMainScreenTest = {
  async churn(count: number) {
    for (let revision = 0; revision < count; revision++) {
      // New objects mimic native status/tree snapshots without changing the
      // authenticated epoch or channel. Neither may remount the controller.
      store.getState().setTree(tree(revision));
      store.getState().setStatus({ ...store.getState().status });
      store.getState().setLevels(-30, -24);
      store.getState().setPingMs(revision % 15);
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  },
  status(next) { store.getState().setStatus({ ...store.getState().status, ...next }); },
  captures() { return captures.map((stream) => stream.getTracks().map((track) => track.readyState)); },
};

createRoot(document.getElementById('root')!).render(
  <React.StrictMode><div className="fixed inset-0"><MainScreen /></div></React.StrictMode>,
);
