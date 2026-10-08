import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';
import { startScreen } from './start-screen.ts';
import {
  isolatedGameAudio,
  mediaTestArguments,
  nativeDisplayTestEnvironment,
} from './linux-audio-fixture.ts';
import type { ScreenQuality } from '../src/renderer/media/screen-settings.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(!fixture, 'Start the isolated REALITY fixture and set GUL_ELECTRON_STAND_DIR.');

interface Probe {
  peers: RTCPeerConnection[];
  captures: MediaStream[];
  requested: { width: number; height: number; frameRate: number }[];
}

/** Render only a synthetic canvas. Linux still exercises the production consent and audio helper. */
async function instrument(page: Page) {
  await page.evaluate((linux) => {
    const probe: Probe = { peers: [], captures: [], requested: [] };
    Object.defineProperty(window, '__gulPresetProbe', { value: probe });
    const Original = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends Original {
      constructor(config?: RTCConfiguration) {
        super(config);
        probe.peers.push(this);
      }
    };
    const nativeDisplay = navigator.mediaDevices.getDisplayMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getDisplayMedia = async (options) => {
      const native = linux ? await nativeDisplay(options) : undefined;
      const requested = typeof options?.video === 'object' ? options.video : {};
      const value = (input: ConstrainDouble | undefined) =>
        typeof input === 'number' ? input : (input?.ideal ?? input?.max ?? 0);
      const width = value(requested.width);
      const height = value(requested.height);
      const frameRate = value(requested.frameRate);
      probe.requested.push({ width, height, frameRate });
      const canvas = document.createElement('canvas');
      canvas.width = width;
      canvas.height = height;
      const context = canvas.getContext('2d')!;
      let frame = 0;
      const draw = () => {
        context.fillStyle = `hsl(${(frame++ * 5) % 360} 60% 40%)`;
        context.fillRect(0, 0, width, height);
        context.fillStyle = '#ffffff';
        context.fillRect((frame * 10) % Math.max(1, width - 80), height / 2, 80, 80);
      };
      draw();
      const timer = setInterval(draw, 1000 / frameRate);
      const stream = canvas.captureStream(frameRate);
      const track = stream.getVideoTracks()[0];
      const stop = track.stop.bind(track);
      track.stop = () => {
        clearInterval(timer);
        stop();
        native?.getTracks().forEach((source) => source.stop());
      };
      probe.captures.push(stream);
      return stream;
    };
  }, process.platform === 'linux');
}

async function publication(page: Page) {
  return page.evaluate(async () => {
    const { peers, captures, requested } = (window as unknown as { __gulPresetProbe: Probe })
      .__gulPresetProbe;
    const video = captures.at(-1)?.getVideoTracks()[0];
    const sender = peers
      .flatMap((peer) => peer.getSenders())
      .find((entry) => entry.track === video && entry.track?.readyState === 'live');
    const parameters = sender?.getParameters();
    const constraints = video?.getConstraints();
    const transports: boolean[] = [];
    let voicePackets = 0;
    for (const peer of peers.filter((entry) => entry.connectionState === 'connected')) {
      const stats = await peer.getStats();
      stats.forEach((entry) => {
        if (entry.type === 'outbound-rtp' && entry.kind === 'audio') voicePackets += entry.packetsSent ?? 0;
        if (entry.type === 'transport' && entry.selectedCandidatePairId) {
          const pair = stats.get(entry.selectedCandidatePairId);
          const candidate = stats.get(pair.localCandidateId);
          transports.push(candidate.candidateType === 'relay' && candidate.relayProtocol === 'tcp');
        }
      });
    }
    return {
      requested: requested.at(-1),
      constraints,
      settings: video?.getSettings(),
      encodings: parameters?.encodings.map((entry) => ({
        maxBitrate: entry.maxBitrate,
        maxFramerate: entry.maxFramerate,
      })),
      relayTcp: transports.length > 0 && transports.every(Boolean),
      voicePackets,
    };
  });
}

test('all four screen presets configure Chromium and send decoded video through REALITY', async () => {
  test.setTimeout(150_000);
  const address = (await readFile(resolve(fixture!, 'address'), 'utf8')).trim();
  const password = (await readFile(resolve(fixture!, 'join-password'), 'utf8')).trim();
  const ca = resolve(fixture!, 'ca.pem');
  const hasCA = await access(ca).then(
    () => true,
    () => false,
  );
  const root = await mkdtemp(join(tmpdir(), 'gul-screen-presets-'));
  const applications: ElectronApplication[] = [];
  let gameAudio: Awaited<ReturnType<typeof isolatedGameAudio>> | undefined;
  try {
    gameAudio = await isolatedGameAudio(2);
    const pages: Page[] = [];
    for (let index = 0; index < 2; index++) {
      const app = await electron.launch({
        executablePath: require('electron'),
        args: [
          '.',
          '--gul-electron-test',
          ...mediaTestArguments,
          `--user-data-dir=${join(root, String(index))}`,
        ],
        env: {
          ...process.env,
          NODE_ENV: 'test',
          ...nativeDisplayTestEnvironment,
          ...gameAudio.environments[index],
          ...(hasCA ? { GUL_ELECTRON_TEST_CA: ca } : {}),
        },
      });
      applications.push(app);
      const page = await app.firstWindow();
      await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
      await instrument(page);
      await privateLoginForm(page, address, password);
      await page.getByLabel('Твой ник', { exact: true }).fill(`preset-peer-${index}`);
      await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
      pages.push(page);
    }
    const [publisher, viewer] = pages;
    const cases: readonly [ScreenQuality, number, number, number, number][] = [
      ['720p30', 1280, 720, 30, 2_000_000],
      ['720p60', 1280, 720, 60, 4_000_000],
      ['1080p30', 1920, 1080, 30, 5_000_000],
      ['1080p60', 1920, 1080, 60, 8_000_000],
    ];
    for (const [quality, width, height, frameRate, maxBitrate] of cases) {
      await startScreen(publisher, quality);
      await expect(
        publisher.getByRole('button', { name: 'Остановить демонстрацию', exact: true }),
      ).toBeVisible({
        timeout: 25_000,
      });
      await viewer.getByRole('button', { name: /preset-peer-0.*Смотреть экран/ }).click();
      const video = viewer.locator('.screen-viewer video');
      await expect(video).toBeVisible();
      await expect
        .poll(
          () =>
            video.evaluate(
              (element: HTMLVideoElement) => element.getVideoPlaybackQuality().totalVideoFrames > 3,
            ),
          { timeout: 20_000 },
        )
        .toBe(true);
      const sent = await publication(publisher);
      expect(sent.requested).toEqual({ width, height, frameRate });
      expect(sent.constraints).toMatchObject({
        width: { max: width },
        height: { max: height },
        frameRate: { max: frameRate },
      });
      expect(sent.settings).toMatchObject({ width, height, frameRate });
      expect(sent.encodings).toEqual([{ maxBitrate, maxFramerate: frameRate }]);
      expect(sent.relayTcp).toBe(true);
      expect(sent.voicePackets).toBeGreaterThan(0);
      const received = await video.evaluate((element: HTMLVideoElement) => ({
        width: element.videoWidth,
        height: element.videoHeight,
      }));
      expect(received.width).toBeGreaterThan(0);
      expect(received.width).toBeLessThanOrEqual(width);
      expect(received.height).toBeGreaterThan(0);
      expect(received.height).toBeLessThanOrEqual(height);
      await publisher.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
      await expect(viewer.locator('.screen-viewer video')).toHaveCount(0);
      for (const page of pages)
        await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible();
    }
    expect(
      await publisher.evaluate(() =>
        (window as unknown as { __gulPresetProbe: Probe }).__gulPresetProbe.captures.every((stream) =>
          stream.getTracks().every((track) => track.readyState === 'ended'),
        ),
      ),
    ).toBe(true);
    console.info(
      'GUL_SCREEN_PRESETS_PROOF',
      JSON.stringify({
        modes: 4,
        configuredFrameCaps: [30, 60],
        decodedFrames: true,
        relayTcp: true,
        voiceConnected: true,
      }),
    );
  } finally {
    try {
      await Promise.all(applications.map((app) => app.close()));
    } finally {
      try {
        await gameAudio?.close();
      } finally {
        await rm(root, { recursive: true, force: true });
      }
    }
  }
});
