import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test';
import { loadRemoteConfig } from './config';

test.skip(process.env.GUL_REMOTE_E2E !== '1', 'Opt-in remote media verification');

interface Owner { sessionToken: string; channelId: number; revision: number }

// Wrap transport failures: Playwright's request errors can include request
// headers. Reporting only the stage keeps credentials out of test artifacts.
async function broker<T>(request: APIRequestContext, url: string, path: string, token = '', data?: unknown): Promise<T> {
  try {
    const response = await request.fetch(`${url}/api/gul${path}`, {
      method: path === '/state' ? 'GET' : 'POST', maxRedirects: 0,
      headers: token ? { Authorization: `Bearer ${token}` } : {}, data, timeout: 15_000,
    });
    if (!response.ok()) throw new Error();
    return response.status() === 204 ? undefined as T : await response.json() as T;
  } catch {
    throw new Error(`Remote broker ${path} failed`);
  }
}

async function client(browser: Browser, request: APIRequestContext, url: string, owner: Owner, forceRelay: boolean, pages: Page[]): Promise<Page> {
  const page = await browser.newPage();
  pages.push(page);
  await page.addInitScript(() => {
    const Peer = window.RTCPeerConnection;
    window.gulRemotePeers = [];
    window.RTCPeerConnection = class extends Peer {
      constructor(config?: RTCConfiguration) { super(config); window.gulRemotePeers.push(this); }
    };
  });
  await page.route('**/test/remote-bootstrap', (route) => route.fulfill({ json: { server: url, channelId: owner.channelId, forceRelay } }));
  await page.route('**/test/remote-screen-grant', async (route) => {
    try {
      const grant = await broker<Record<string, unknown>>(request, url, '/screen', owner.sessionToken, {
        channelId: owner.channelId, revision: owner.revision,
      });
      await route.fulfill({ json: { ...grant, epoch: 1 } });
    } catch {
      await route.fulfill({ status: 503 });
    }
  });
  await page.goto('/e2e/remote/index.html');
  await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
  await expect(page.getByTestId('remote-status')).toHaveText('connected');
  return page;
}

for (const forceRelay of [false, true]) {
  test(`authenticated remote screen video and audio (${forceRelay ? 'TURN TLS 443 only' : 'normal ICE'})`, async ({ browser, request }) => {
    const { url, password } = loadRemoteConfig(process.env.GUL_REMOTE_E2E_ENV);
    const owners: Owner[] = [];
    const pages: Page[] = [];
    let leaseFailure = false;
    const lease = setInterval(() => {
      for (const owner of owners) void broker(request, url, '/state', owner.sessionToken).catch(() => { leaseFailure = true; });
    }, 15_000);
    try {
      for (const role of ['publisher', 'viewer']) {
        owners.push(await broker<Owner>(request, url, '/login', '', {
          username: `media-test-${role}-${Date.now().toString(36)}`, password,
        }));
      }
      for (const owner of owners) await client(browser, request, url, owner, forceRelay, pages);
      const [publisher, viewer] = pages;
      await publisher.getByRole('button', { name: 'Картинка и тон', exact: true }).click();
      const video = viewer.locator('video[data-local="false"]');
      const audio = viewer.locator('audio');
      await expect(video).toHaveCount(1);
      await expect(audio).toHaveCount(1);
      await viewer.getByRole('button', { name: 'Разрешить звук', exact: true }).click();
      await video.scrollIntoViewIfNeeded();
      await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.videoWidth)).toBe(1280);
      const frames = await video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames);
      await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames)).toBeGreaterThan(frames + 10);
      const background = await video.evaluate((el: HTMLVideoElement) => {
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = 1;
        const context = canvas.getContext('2d')!;
        context.drawImage(el, 10, 200, 1, 1, 0, 0, 1, 1);
        return [...context.getImageData(0, 0, 1, 1).data].slice(0, 3);
      });
      for (const [index, expected] of [27, 36, 64].entries()) expect(Math.abs(background[index] - expected)).toBeLessThan(12);
      const signal = await audio.evaluate(async (el: HTMLAudioElement) => {
        const context = new AudioContext();
        await context.resume();
        const source = context.createMediaStreamSource(el.srcObject as MediaStream);
        const analyser = context.createAnalyser();
        analyser.fftSize = 4096;
        analyser.smoothingTimeConstant = 0;
        source.connect(analyser);
        const samples = new Float32Array(analyser.fftSize);
        const spectrum = new Float32Array(analyser.frequencyBinCount);
        let peak = 0;
        let frequency = 0;
        try {
          for (let index = 0; index < 20; index++) {
            await new Promise((resolve) => setTimeout(resolve, 50));
            analyser.getFloatTimeDomainData(samples);
            const current = Math.max(...samples.map(Math.abs));
            if (current > peak) {
              peak = current;
              analyser.getFloatFrequencyData(spectrum);
              const bin = spectrum.indexOf(Math.max(...spectrum));
              frequency = bin * context.sampleRate / analyser.fftSize;
            }
          }
        } finally { await context.close(); }
        return { peak, frequency };
      });
      expect(signal.peak).toBeGreaterThan(0.01);
      expect(Math.abs(signal.frequency - 440)).toBeLessThan(15);
      if (forceRelay) for (const page of pages) {
        const selected = await page.evaluate(() => window.gulRemoteTransports());
        expect(selected.length).toBeGreaterThan(0);
        expect(selected.every((candidate) => candidate.relay && candidate.turnTls443)).toBe(true);
      }
      await expect(publisher.locator('audio')).toHaveCount(0);
      await publisher.getByRole('button', { name: 'Остановить', exact: true }).click();
      await expect(video).toHaveCount(0);
      await expect(audio).toHaveCount(0);
      expect(leaseFailure).toBe(false);
    } finally {
      clearInterval(lease);
      await Promise.allSettled(pages.map((page) => page.close()));
      await Promise.allSettled(owners.map((owner) => broker(request, url, '/logout', owner.sessionToken)));
    }
  });
}
