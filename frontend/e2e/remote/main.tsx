import React, { useEffect, useState, useSyncExternalStore } from 'react';
import { createRoot } from 'react-dom/client';
import { LiveKitController } from '../../src/livekit/controller';
import { MediaTile } from '../../src/livekit/MediaTile';
import { createTestPattern } from '../../src/livekit/pattern';
import { screenSession, sessionGrantProvider, type ScreenGrant } from '../../src/livekit/session';
import '../../src/style.css';

interface Bootstrap { server: string; channelId: number; forceRelay: boolean }
interface CandidateSummary { relay: boolean; turnTls443: boolean }
declare global { interface Window { gulRemotePeers: RTCPeerConnection[]; gulRemoteTransports(): Promise<CandidateSummary[]> } }

// Selected candidate summaries deliberately exclude credentials, full candidate
// addresses, SDP and JWTs. No getDisplayMedia or microphone APIs are used here.
window.gulRemoteTransports = async () => {
  const summaries: CandidateSummary[] = [];
  for (const peer of window.gulRemotePeers) {
    const stats = await peer.getStats();
    stats.forEach((report) => {
      if (report.type !== 'transport' || !report.selectedCandidatePairId) return;
      const pair = stats.get(report.selectedCandidatePairId);
      const candidate = pair && stats.get(pair.localCandidateId);
      if (!candidate) return;
      summaries.push({
        relay: candidate.candidateType === 'relay',
        turnTls443: candidate.relayProtocol === 'tls' &&
          /^turns:(?:\/\/)?(?:\[[^\]]+\]|[^:/?]+):443(?:\?|$)/.test(candidate.url ?? ''),
      });
    });
  }
  return summaries;
};

function RemoteMedia({ config }: { config: Bootstrap }) {
  const [controller] = useState(() => {
    const session = screenSession({ state: 'connected', server: config.server, selfChannel: config.channelId, epoch: 1 });
    if (!session) throw new Error('Invalid remote test session');
    return new LiveKitController(sessionGrantProvider(session, async () => {
      const response = await fetch('/test/remote-screen-grant', { method: 'POST' });
      if (!response.ok) throw new Error('Remote grant unavailable');
      return await response.json() as ScreenGrant;
    }), { allowServerIce: true, forceRelay: config.forceRelay });
  });
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  useEffect(() => () => { void controller.leave(); }, [controller]);
  return <main className="p-5">
    <p data-testid="remote-status">{snapshot.status}</p>
    <button onClick={() => void controller.join('')}>Подключиться</button>
    <button disabled={snapshot.status !== 'connected'} onClick={() => void controller.shareWith(createTestPattern)}>Картинка и тон</button>
    <button onClick={() => void controller.startAudio()}>Разрешить звук</button>
    <button onClick={() => void controller.stopShare()}>Остановить</button>
    <button onClick={() => void controller.leave()}>Выйти</button>
    {snapshot.error && <p role="alert">{snapshot.error}</p>}
    <section className="grid max-w-3xl gap-3">{snapshot.tracks.map((item) => <MediaTile key={item.id} item={item} />)}</section>
  </main>;
}

fetch('/test/remote-bootstrap').then(async (response) => {
  const config = await response.json() as Bootstrap;
  createRoot(document.getElementById('root')!).render(<React.StrictMode><RemoteMedia config={config} /></React.StrictMode>);
}).catch(() => { document.getElementById('root')!.textContent = 'Remote test configuration unavailable'; });
