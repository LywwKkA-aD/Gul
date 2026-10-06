import type { Page } from '@playwright/test';

declare global { interface Window { gulCompanionCaptures: MediaStream[]; gulCompanionPeers: RTCPeerConnection[] } }

export async function syntheticCapture(page: Page) {
  await page.addInitScript(() => {
    window.gulCompanionCaptures = [];
    window.gulCompanionPeers = [];
    const Peer = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends Peer {
      constructor(config?: RTCConfiguration) { super(config); window.gulCompanionPeers.push(this); }
    };
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
      const audio = new AudioContext({ sampleRate: 48000 });
      const destination = audio.createMediaStreamDestination();
      const oscillator = audio.createOscillator();
      const gain = audio.createGain();
      oscillator.frequency.value = 440;
      gain.gain.value = 0.06;
      oscillator.connect(gain).connect(destination);
      oscillator.start();
      await audio.resume();
      destination.stream.getAudioTracks().forEach((track) => stream.addTrack(track));
      const stops = stream.getTracks().map((track) => track.stop.bind(track));
      let closed = false;
      stream.getTracks().forEach((track) => { track.stop = () => {
        if (closed) return;
        closed = true;
        clearInterval(timer);
        oscillator.stop();
        void audio.close();
        stops.forEach((stop) => stop());
      }; });
      window.gulCompanionCaptures.push(stream);
      return stream;
    };
  });
}

export const captureStates = (page: Page) => page.evaluate(() => window.gulCompanionCaptures.flatMap((stream) => stream.getTracks().map((track) => track.readyState)));

/** Report only booleans: full WebRTC stats can contain TURN credentials. */
export const realityRoutes = (page: Page) => page.evaluate(async () => {
  const localTurn = (url: string) => /^turn:127\.0\.0\.1:\d+\?transport=tcp$/.test(url);
  const peers = [];
  for (const peer of window.gulCompanionPeers) {
    const config = peer.getConfiguration();
    const servers = (config.iceServers ?? []).flatMap((server) => typeof server.urls === 'string' ? [server.urls] : server.urls);
    const selected: boolean[] = [];
    const stats = await peer.getStats();
    stats.forEach((report) => {
      if (report.type !== 'transport' || !report.selectedCandidatePairId) return;
      const pair = stats.get(report.selectedCandidatePairId);
      const candidate = pair && stats.get(pair.localCandidateId);
      if (candidate) selected.push(candidate.candidateType === 'relay' && candidate.relayProtocol === 'tcp' && localTurn(candidate.url ?? ''));
    });
    peers.push({ relayOnly: config.iceTransportPolicy === 'relay', onlyLocalTurn: servers.length > 0 && servers.every(localTurn), selected });
  }
  return peers;
});
