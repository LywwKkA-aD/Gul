import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';
import { join, resolve } from 'node:path';
import {
  isolatedGameAudio,
  mediaTestArguments,
  nativeDisplayTestEnvironment,
} from './linux-audio-fixture.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;

test('Chromium applies VP8 placeholder conformance while preserving the screen bitrate', async () => {
  const dataRoot = await mkdtemp(join(tmpdir(), 'gul-sdp-browser-'));
  const app = await electron.launch({
    executablePath: require('electron'),
    args: ['.', '--gul-electron-test', `--user-data-dir=${dataRoot}`],
    env: { ...process.env, NODE_ENV: 'test' },
  });
  try {
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    const helper = await build({
      stdin: {
        contents:
          "import { conformVp8Placeholders } from './src/renderer/media/sdp-bundle.ts'; globalThis.__gulConform=conformVp8Placeholders;",
        resolveDir: process.cwd(),
      },
      bundle: true,
      format: 'iife',
      platform: 'browser',
      write: false,
    });
    await page.evaluate(helper.outputFiles[0].text);
    const result = await page.evaluate(async () => {
      const normalize = (globalThis as unknown as { __gulConform(sdp: string, mids: Set<string>): string })
        .__gulConform;
      const offer = async (repair: boolean) => {
        const peer = new RTCPeerConnection();
        const receiver = new RTCPeerConnection();
        const canvas = document.createElement('canvas');
        canvas.width = 1280;
        canvas.height = 720;
        canvas.getContext('2d')!.fillRect(0, 0, 1280, 720);
        const stream = canvas.captureStream(30);
        const active = peer.addTransceiver(stream.getVideoTracks()[0], { direction: 'sendonly' });
        const placeholder = peer.addTransceiver('video', { direction: 'recvonly' });
        for (const transceiver of [active, placeholder]) {
          const vp8 = RTCRtpSender.getCapabilities('video')!.codecs.filter(
            (codec) => codec.mimeType.toLowerCase() === 'video/vp8',
          );
          transceiver.setCodecPreferences(vp8);
        }
        try {
          const initial = await peer.createOffer();
          await peer.setLocalDescription(initial);
          await receiver.setRemoteDescription(initial);
          const outgoing = receiver.getTransceivers()[1];
          await outgoing.sender.replaceTrack(stream.getVideoTracks()[0]);
          outgoing.direction = 'sendonly';
          const answer = await receiver.createAnswer();
          await receiver.setLocalDescription(answer);
          await peer.setRemoteDescription(answer);
          const raw = await peer.createOffer();
          const parts = raw.sdp!.split(/(?=m=)/);
          const real = parts.findIndex((section) => section.startsWith('m=video'));
          const payload = /^a=rtpmap:(\d+) VP8\/90000$/im.exec(parts[real])![1];
          const mid = /^a=mid:(.+)$/m.exec(parts[real + 1])![1].trim();
          parts[real] += `a=fmtp:${payload} x-google-start-bitrate=1800\r\n`;
          const broken = parts.join('');
          const sdp = repair ? normalize(broken, new Set([mid])) : broken;
          let collision = false;
          let applied = false;
          try {
            await peer.setLocalDescription({ type: 'offer', sdp });
            await receiver.setRemoteDescription({ type: 'offer', sdp });
          } catch (error) {
            collision = /bundled payload type collision|codec collision/i.test(String(error));
          }
          applied = receiver.remoteDescription?.sdp.includes('x-google-start-bitrate=1800') ?? false;
          const configs = sdp
            .split(/(?=m=)/)
            .filter((part) => part.startsWith('m=video'))
            .map((part) => /^a=fmtp:96 (.+)$/m.exec(part)?.[1].trim() ?? '');
          return {
            collision,
            applied,
            preservedHint: receiver.remoteDescription?.sdp.includes('x-google-start-bitrate=1800') ?? false,
            conformed: configs.every((value) => value === configs[0]),
          };
        } finally {
          stream.getTracks().forEach((track) => track.stop());
          peer.close();
          receiver.close();
        }
      };
      return { before: await offer(false), after: await offer(true) };
    });
    // macOS libwebrtc accepts this mismatch; Ubuntu's logged INVALID_PARAMETER rejects it.
    expect(result.before.conformed).toBe(false);
    expect(result.after.conformed).toBe(true);
    expect(result.after.collision).toBe(false);
    expect(result.after.applied).toBe(true);
    expect(result.after.preservedHint).toBe(true);
  } finally {
    await app.close();
    await rm(dataRoot, { recursive: true, force: true });
  }
});

async function instrument(page: Page) {
  await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
  await page.evaluate((linux) => {
    const nativeDisplay = navigator.mediaDevices.getDisplayMedia.bind(navigator.mediaDevices);
    const original = window.RTCPeerConnection;
    const peers: RTCPeerConnection[] = [];
    const failures: string[] = [];
    const mismatches: boolean[] = [];
    const records: {
      side: string;
      type: string;
      mismatch: boolean;
      nullMids: number;
      retained: number;
      hint: boolean;
    }[] = [];
    Object.defineProperty(window, '__gulSDPPeers', { value: peers });
    Object.defineProperty(window, '__gulSDPFailures', { value: failures });
    Object.defineProperty(window, '__gulSDPMismatches', { value: mismatches });
    Object.defineProperty(window, '__gulSDPRecords', { value: records });
    const observe = (peer: RTCPeerConnection, description: RTCLocalSessionDescriptionInit, side: string) => {
      if (!description.sdp || !description.type || !['offer', 'answer'].includes(description.type)) return;
      const parts = description.sdp.split(/(?=m=)/);
      const groups = [...parts[0].matchAll(/^a=group:BUNDLE (.+)$/gm)].map(
        (match) => new Set(match[1].trim().split(/\s+/)),
      );
      const video = parts.filter(
        (part) => /^m=video /.test(part) && (!/^m=video 0\b/.test(part) || /^a=bundle-only\r?$/m.test(part)),
      );
      const codecs = video.flatMap((part) => {
        const mid = /^a=mid:(.+)$/m.exec(part)?.[1].trim();
        const payload = /^a=rtpmap:(\d+) VP8\/90000\r?$/im.exec(part)?.[1];
        if (!mid || !payload) return [];
        const config = new RegExp(`^a=fmtp:${payload} (.+)$`, 'm').exec(part)?.[1].trim() ?? '';
        return [{ mid, payload, config }];
      });
      const mismatch = groups.some((group) => {
        const bundled = codecs.filter((codec) => group.has(codec.mid));
        return bundled.some((codec) =>
          bundled.some((other) => other.payload === codec.payload && other.config !== codec.config),
        );
      });
      if (mismatch) mismatches.push(true);
      const transceivers = peer.getTransceivers();
      records.push({
        side,
        type: description.type,
        mismatch,
        nullMids: transceivers.filter((t) => t.mid === null).length,
        retained: transceivers.filter(
          (t) =>
            Boolean(t.sender.track) && (t.direction === 'inactive' || t.sender.track?.readyState === 'ended'),
        ).length,
        hint: description.sdp.includes('x-google-start-bitrate=1800'),
      });
    };
    window.RTCPeerConnection = class extends original {
      constructor(config?: RTCConfiguration) {
        super(config);
        peers.push(this);
      }
      async setLocalDescription(
        description?: RTCLocalSessionDescriptionInit,
        success?: VoidFunction,
        failure?: RTCPeerConnectionErrorCallback,
      ) {
        if (description) observe(this, description, 'local');
        try {
          return await (success && failure
            ? super.setLocalDescription(description ?? {}, success, failure)
            : super.setLocalDescription(description));
        } catch (error) {
          failures.push(
            /bundled payload type collision|codec collision/i.test(String(error))
              ? 'VP8 BUNDLE collision'
              : 'Description rejected',
          );
          throw error;
        }
      }
      async setRemoteDescription(
        description: RTCSessionDescriptionInit,
        success?: VoidFunction,
        failure?: RTCPeerConnectionErrorCallback,
      ) {
        observe(this, description, 'remote');
        try {
          return await (success && failure
            ? super.setRemoteDescription(description, success, failure)
            : super.setRemoteDescription(description));
        } catch (error) {
          failures.push(
            /bundled payload type collision|codec collision/i.test(String(error))
              ? 'VP8 BUNDLE collision'
              : 'Description rejected',
          );
          throw error;
        }
      }
    };
    navigator.mediaDevices.getDisplayMedia = async (options) => {
      const native = linux ? await nativeDisplay(options) : undefined;
      const canvas = document.createElement('canvas');
      canvas.width = 1280;
      canvas.height = 720;
      const context = canvas.getContext('2d')!;
      let frame = 0;
      const timer = setInterval(() => {
        context.fillStyle = `hsl(${frame++ % 360} 60% 40%)`;
        context.fillRect(0, 0, 1280, 720);
      }, 33);
      const stream = canvas.captureStream(30);
      const audio = linux ? undefined : new AudioContext({ sampleRate: 48000 });
      if (audio) {
        const output = audio.createMediaStreamDestination();
        const oscillator = audio.createOscillator();
        oscillator.frequency.value = 440;
        const gain = audio.createGain();
        gain.gain.value = 0.1;
        oscillator.connect(gain).connect(output);
        oscillator.start();
        await audio.resume();
        stream.addTrack(output.stream.getAudioTracks()[0]);
      }
      const video = stream.getVideoTracks()[0];
      const stop = video.stop.bind(video);
      video.stop = () => {
        clearInterval(timer);
        stop();
        void audio?.close();
        native?.getTracks().forEach((track) => track.stop());
      };
      return stream;
    };
  }, process.platform === 'linux');
}
async function decoded(page: Page) {
  await expect
    .poll(
      () =>
        page
          .locator('.screen-viewer video')
          .evaluateAll((elements) =>
            elements.some(
              (element) => (element as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames > 10,
            ),
          ),
      { timeout: 20_000 },
    )
    .toBe(true);
}

test('solo REALITY publisher keeps local offers and remote answers conformed over repeated screen rooms', async () => {
  test.skip(!fixture, 'Set GUL_ELECTRON_STAND_DIR to the isolated REALITY fixture.');
  const address = (await readFile(resolve(fixture!, 'address'), 'utf8')).trim();
  const password = (await readFile(resolve(fixture!, 'join-password'), 'utf8')).trim();
  const ca = resolve(fixture!, 'ca.pem');
  const dataRoot = await mkdtemp(join(tmpdir(), 'gul-sdp-solo-'));
  const gameAudio = await isolatedGameAudio();
  let app: ElectronApplication | undefined;
  try {
    app = await electron.launch({
      executablePath: require('electron'),
      args: ['.', '--gul-electron-test', ...mediaTestArguments, `--user-data-dir=${dataRoot}`],
      env: {
        ...process.env,
        NODE_ENV: 'test',
        ...nativeDisplayTestEnvironment,
        ...gameAudio.environments[0],
        GUL_ELECTRON_TEST_CA: ca,
      },
    });
    const page = await app.firstWindow();
    page.on('console', (message) => {
      if (/^GUL_(SCREEN|CAPTURE)_FAILURE [A-Za-z ]+$/.test(message.text())) console.info(message.text());
    });
    await instrument(page);
    await privateLoginForm(page, address, password);
    await page.getByLabel('Твой ник', { exact: true }).fill('sdp-solo');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    await expect(page.locator('.channel-users > button')).toHaveCount(1);
    for (let cycle = 0; cycle < 12; cycle++) {
      await expect(page.locator('.channel-users > button')).toHaveCount(1);
      await page.getByRole('button', { name: 'Показать экран', exact: true }).click();
      try {
        await expect(page.getByRole('button', { name: 'Остановить демонстрацию', exact: true })).toBeVisible({
          timeout: 20_000,
        });
      } catch (error) {
        console.info(
          'GUL_SDP_SOLO_FAILED',
          await page.evaluate(() => {
            const records = (
              window as unknown as { __gulSDPRecords: { side: string; type: string; mismatch: boolean }[] }
            ).__gulSDPRecords;
            return {
              localMismatches: records.filter((r) => r.side === 'local' && r.mismatch).length,
              remoteOfferMismatches: records.filter(
                (r) => r.side === 'remote' && r.type === 'offer' && r.mismatch,
              ).length,
              remoteAnswerMismatches: records.filter(
                (r) => r.side === 'remote' && r.type === 'answer' && r.mismatch,
              ).length,
              descriptionRejections: (window as unknown as { __gulSDPFailures: string[] }).__gulSDPFailures
                .length,
            };
          }),
        );
        throw error;
      }
      await expect
        .poll(() =>
          page
            .locator('video')
            .evaluateAll((videos) =>
              videos.some(
                (video) => (video as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames > 3,
              ),
            ),
        )
        .toBe(true);
      await page.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
      await expect(page.locator('video')).toHaveCount(0);
      if (cycle % 3 === 2) {
        await page.getByRole('button', { name: cycle % 2 ? 'Игра' : 'Общая', exact: true }).click();
        await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible();
      }
    }
    const facts = await page.evaluate(() => {
      const scope = window as unknown as {
        __gulSDPRecords: {
          side: string;
          type: string;
          mismatch: boolean;
          nullMids: number;
          retained: number;
          hint: boolean;
        }[];
        __gulSDPFailures: string[];
      };
      return {
        localOfferMismatches: scope.__gulSDPRecords.filter(
          (r) => r.side === 'local' && r.type === 'offer' && r.mismatch,
        ).length,
        remoteOfferMismatches: scope.__gulSDPRecords.filter(
          (r) => r.side === 'remote' && r.type === 'offer' && r.mismatch,
        ).length,
        remoteAnswerMismatches: scope.__gulSDPRecords.filter(
          (r) => r.side === 'remote' && r.type === 'answer' && r.mismatch,
        ).length,
        firstOfferHints: scope.__gulSDPRecords.filter((r) => r.side === 'local' && r.nullMids > 0 && r.hint)
          .length,
        hintedOffers: scope.__gulSDPRecords.filter((r) => r.side === 'local' && r.type === 'offer' && r.hint)
          .length,
        collisionFailures: scope.__gulSDPFailures.filter((failure) => failure === 'VP8 BUNDLE collision')
          .length,
        descriptionRejections: scope.__gulSDPFailures.length,
      };
    });
    console.info('GUL_SDP_SOLO_PROOF', JSON.stringify(facts));
    expect(facts.hintedOffers).toBeGreaterThan(10);
    expect(facts.localOfferMismatches).toBe(0);
    expect(facts.remoteOfferMismatches).toBe(0);
    expect(facts.remoteAnswerMismatches).toBe(0);
    expect(facts.collisionFailures).toBe(0);
    expect(facts.descriptionRejections).toBe(0);
  } finally {
    try {
      await app?.close();
    } finally {
      await gameAudio.close();
      await rm(dataRoot, { recursive: true, force: true });
    }
  }
});

test('three REALITY clients publish, late join, watch, restart and rejoin without BUNDLE fallback', async () => {
  test.skip(!fixture, 'Set GUL_ELECTRON_STAND_DIR to the isolated REALITY fixture.');
  const address = (await readFile(resolve(fixture!, 'address'), 'utf8')).trim();
  const password = (await readFile(resolve(fixture!, 'join-password'), 'utf8')).trim();
  const ca = resolve(fixture!, 'ca.pem');
  const hasCA = await access(ca).then(
    () => true,
    () => false,
  );
  const dataRoot = await mkdtemp(join(tmpdir(), 'gul-sdp-two-peer-'));
  const apps: ElectronApplication[] = [];
  const pages: Page[] = [];
  let stopGameAudio = async () => {};
  try {
    const gameAudio = await isolatedGameAudio(3);
    stopGameAudio = gameAudio.close;
    const connectPeer = async (i: number) => {
      const app = await electron.launch({
        executablePath: require('electron'),
        args: [
          '.',
          '--gul-electron-test',
          ...mediaTestArguments,
          `--user-data-dir=${join(dataRoot, String(i))}`,
        ],
        env: {
          ...process.env,
          NODE_ENV: 'test',
          ...nativeDisplayTestEnvironment,
          ...gameAudio.environments[i],
          ...(hasCA ? { GUL_ELECTRON_TEST_CA: ca } : {}),
        },
      });
      apps.push(app);
      const page = await app.firstWindow();
      pages.push(page);
      await instrument(page);
      await privateLoginForm(page, address, password);
      await page.getByLabel('Твой ник', { exact: true }).fill(`sdp-peer-${i}`);
      await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
      return page;
    };
    for (let i = 0; i < 2; i++) await connectPeer(i);
    const [a, b] = pages;
    await Promise.all(
      pages.map((page) => page.getByRole('button', { name: 'Показать экран', exact: true }).click()),
    );
    await a.getByRole('button', { name: /sdp-peer-1.*Смотреть экран/ }).click();
    await b.getByRole('button', { name: /sdp-peer-0.*Смотреть экран/ }).click();
    await Promise.all(pages.map(decoded));
    const c = await connectPeer(2);
    await c.getByRole('button', { name: /sdp-peer-0.*Смотреть экран/ }).click();
    await decoded(c);
    await c.getByRole('button', { name: 'Показать экран', exact: true }).click();
    await a.getByRole('button', { name: /sdp-peer-2.*Смотреть экран/ }).click();
    await decoded(a);
    for (let cycle = 0; cycle < 3; cycle++) {
      await a.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
      await expect(b.locator('.screen-viewer video')).toHaveCount(0);
      await a.getByRole('button', { name: 'Показать экран', exact: true }).click();
      await b.getByRole('button', { name: /sdp-peer-0.*Смотреть экран/ }).click();
      await decoded(b);
      await c.getByRole('button', { name: /sdp-peer-0.*Смотреть экран/ }).click();
      await decoded(c);
      await b.getByRole('button', { name: 'Игра', exact: true }).click();
      await expect(b.getByText('Голос подключён', { exact: true })).toBeVisible();
      await b.getByRole('button', { name: 'Общая', exact: true }).click();
      await b.getByRole('button', { name: /sdp-peer-0.*Смотреть экран/ }).click();
      await decoded(b);
      if (cycle === 1) {
        await c.getByRole('button', { name: 'Отключиться', exact: true }).click();
        await expect(c.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
        await privateLoginForm(c, address, password);
        await c.getByRole('button', { name: 'Подключиться', exact: true }).click();
        await expect(c.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
        await c.getByRole('button', { name: /sdp-peer-0.*Смотреть экран/ }).click();
        await decoded(c);
      }
    }
    for (const page of pages) {
      expect(
        await page.evaluate(() => (window as unknown as { __gulSDPFailures: string[] }).__gulSDPFailures),
      ).toEqual([]);
      expect(
        await page.evaluate(
          () => (window as unknown as { __gulSDPMismatches: boolean[] }).__gulSDPMismatches,
        ),
      ).toEqual([]);
      const relays = await page.evaluate(async () => {
        const peers = (window as unknown as { __gulSDPPeers: RTCPeerConnection[] }).__gulSDPPeers;
        const values: boolean[] = [];
        for (const peer of peers.filter((value) => value.connectionState === 'connected')) {
          const stats = await peer.getStats();
          stats.forEach((value) => {
            if (value.type === 'transport' && value.selectedCandidatePairId) {
              const pair = stats.get(value.selectedCandidatePairId);
              const candidate = stats.get(pair.localCandidateId);
              values.push(candidate.candidateType === 'relay' && candidate.relayProtocol === 'tcp');
            }
          });
        }
        return values;
      });
      expect(relays.length).toBeGreaterThan(0);
      expect(relays.every(Boolean)).toBe(true);
    }
    console.info(
      'GUL_SDP_THREE_PEER_PROOF',
      JSON.stringify({
        peers: 3,
        publicationRestarts: 3,
        lateJoins: 1,
        fullRejoins: 1,
        channelRejoins: 3,
        mismatches: 0,
        collisionFailures: 0,
        relayTcp: true,
      }),
    );
  } finally {
    try {
      await Promise.all(apps.map((app) => app.close()));
    } finally {
      try {
        await stopGameAudio();
      } finally {
        await rm(dataRoot, { recursive: true, force: true });
      }
    }
  }
});
