import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import { execFile, spawn, type ChildProcess } from 'node:child_process';
import { promisify } from 'node:util';
import { once } from 'node:events';
import { readFile, mkdtemp, rm, access, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';

const require = createRequire(import.meta.url);
const exec = promisify(execFile);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(
  process.platform !== 'linux' || !fixture || process.env.GUL_ELECTRON_NATIVE_CAPTURE !== '1',
  'Requires an isolated Linux desktop, running PulseAudio, REALITY fixture and explicit native capture opt-in.',
);

async function pulse(...args: string[]): Promise<string> {
  return (await exec('pactl', args, { timeout: 5000, maxBuffer: 64 * 1024 })).stdout.trim();
}

/** The test plays real stereo PCM into PulseAudio; it never replaces a browser capture API. */
async function startTone(directory: string, sink: string): Promise<ChildProcess> {
  const rate = 48000;
  const seconds = 20;
  const pcm = Buffer.alloc(rate * seconds * 4);
  for (let sample = 0; sample < rate * seconds; sample++) {
    pcm.writeInt16LE(Math.round(3276 * Math.sin((2 * Math.PI * 440 * sample) / rate)), sample * 4);
    pcm.writeInt16LE(Math.round(3276 * Math.sin((2 * Math.PI * 880 * sample) / rate)), sample * 4 + 2);
  }
  const path = join(directory, 'stereo.pcm');
  await writeFile(path, pcm, { mode: 0o600 });
  const player = spawn(
    'paplay',
    ['--raw', '--format=s16le', '--rate=48000', '--channels=2', `--device=${sink}`, path],
    {
      stdio: 'ignore',
    },
  );
  await once(player, 'spawn');
  return player;
}

async function remoteStereo(page: Page) {
  return page.evaluate(async () => {
    const element = document.querySelector<HTMLAudioElement>('audio[data-source="screen"]');
    if (!element?.srcObject) return { peak: 0, separation: -100 };
    const context = new AudioContext({ sampleRate: 48000 });
    const source = context.createMediaStreamSource(element.srcObject as MediaStream);
    const splitter = context.createChannelSplitter(2);
    const analysers = [context.createAnalyser(), context.createAnalyser()];
    source.connect(splitter);
    analysers.forEach((analyser, channel) => {
      analyser.fftSize = 4096;
      splitter.connect(analyser, channel);
    });
    try {
      await context.resume();
      await new Promise((resolve) => setTimeout(resolve, 300));
      let peak = 0;
      const energies = analysers.map((analyser) => {
        const samples = new Float32Array(analyser.fftSize);
        analyser.getFloatTimeDomainData(samples);
        peak = Math.max(peak, ...samples.map(Math.abs));
        const spectrum = new Float32Array(analyser.frequencyBinCount);
        analyser.getFloatFrequencyData(spectrum);
        return [440, 880].map((frequency) => {
          const bin = Math.round((frequency * analyser.fftSize) / context.sampleRate);
          return Math.max(...spectrum.slice(bin - 2, bin + 3));
        });
      });
      const separation = Math.min(energies[0][0] - energies[0][1], energies[1][1] - energies[1][0]);
      return { peak, separation: Number.isFinite(separation) ? separation : -100 };
    } finally {
      source.disconnect();
      splitter.disconnect();
      analysers.forEach((analyser) => analyser.disconnect());
      await context.close();
    }
  });
}

async function instrumentPeers(page: Page) {
  await page.evaluate(() => {
    const peers: RTCPeerConnection[] = [];
    Object.defineProperty(window, '__gulNativePeers', { value: peers });
    const Original = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends Original {
      constructor(configuration?: RTCConfiguration) {
        super(configuration);
        peers.push(this);
      }
    };
  });
}

async function selectedRelay(page: Page): Promise<boolean> {
  return page.evaluate(async () => {
    const peers = (window as unknown as { __gulNativePeers: RTCPeerConnection[] }).__gulNativePeers;
    const candidates: { type: string; protocol: string }[] = [];
    for (const peer of peers) {
      if (peer.connectionState !== 'connected') continue;
      const stats = await peer.getStats();
      stats.forEach((stat) => {
        if (stat.type !== 'transport' || !stat.selectedCandidatePairId) return;
        const pair = stats.get(stat.selectedCandidatePairId);
        const local = pair && stats.get(pair.localCandidateId);
        if (local) candidates.push({ type: local.candidateType, protocol: local.relayProtocol });
      });
    }
    return (
      candidates.length > 0 &&
      candidates.every((candidate) => candidate.type === 'relay' && candidate.protocol === 'tcp')
    );
  });
}

async function movingDesktop(page: Page): Promise<boolean> {
  return page.locator('video').evaluate(async (element) => {
    const video = element as HTMLVideoElement;
    const canvas = document.createElement('canvas');
    canvas.width = 32;
    canvas.height = 18;
    const context = canvas.getContext('2d')!;
    const sample = () => {
      context.drawImage(video, 0, 0, canvas.width, canvas.height);
      return context.getImageData(0, 0, canvas.width, canvas.height).data;
    };
    const before = sample();
    await new Promise((resolve) => setTimeout(resolve, 250));
    const after = sample();
    let changed = 0;
    for (let offset = 0; offset < after.length; offset += 4)
      if (Math.abs(after[offset] - before[offset]) > 20) changed++;
    return changed > 10;
  });
}

test('native Linux desktop and PulseAudio stereo loopback reach another Electron client through REALITY', async () => {
  const directory = resolve(fixture!);
  const address = (await readFile(join(directory, 'address'), 'utf8')).trim();
  const password = (await readFile(join(directory, 'join-password'), 'utf8')).trim();
  const caFile = join(directory, 'ca.pem');
  const ca = await access(caFile)
    .then(() => caFile)
    .catch(() => undefined);
  const dataRoot = await mkdtemp(join(tmpdir(), 'gul-native-capture-'));
  const sink = `gul_native_${process.pid}`;
  const apps: ElectronApplication[] = [];
  let oldSink: string | undefined;
  let module: string | undefined;
  let player: ChildProcess | undefined;
  try {
    oldSink = await pulse('get-default-sink');
    module = await pulse('load-module', 'module-null-sink', `sink_name=${sink}`, 'channels=2', 'rate=48000');
    await pulse('set-default-sink', sink);
    const pages: Page[] = [];
    for (let i = 0; i < 2; i++) {
      const app = await electron.launch({
        executablePath: require('electron'),
        args: ['.', '--gul-electron-test', `--user-data-dir=${join(dataRoot, String(i))}`],
        env: {
          ...process.env,
          NODE_ENV: 'test',
          ...(ca ? { GUL_ELECTRON_TEST_CA: ca } : {}),
          GUL_ELECTRON_TEST_CAPTURE_APPROVED: '1',
        },
      });
      apps.push(app);
      const page = await app.firstWindow();
      pages.push(page);
      await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
      await instrumentPeers(page);
      await page.getByLabel('Адрес сервера', { exact: true }).fill(address);
      await page.getByLabel('Твой ник', { exact: true }).fill(i ? 'native-viewer' : 'native-publisher');
      await page.getByLabel('Пароль', { exact: true }).fill(password);
      await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
      // Both applications share this test's null sink. Avoid digitally recapturing viewer playback.
      await page.getByRole('button', { name: 'Выключить звук', exact: true }).click();
    }
    const [publisher, viewer] = pages;
    const capabilities = await publisher.evaluate(() => window.gul.captureCapabilities());
    expect(capabilities).toMatchObject({
      platform: 'linux',
      backend: 'pipewire-pulse',
      systemAudio: true,
      ownAudioExcluded: false,
      audioServer: 'detected',
    });
    player = await startTone(dataRoot, sink);
    await publisher.getByRole('button', { name: 'Показать экран', exact: true }).click();
    const watch = viewer.getByRole('button', { name: /native-publisher.*Смотреть экран/ });
    await expect(watch).toBeVisible({ timeout: 20_000 });
    await watch.click();
    await publisher.evaluate(() => {
      const animation = document.createElement('div');
      animation.setAttribute('aria-hidden', 'true');
      animation.style.cssText =
        'position:fixed;inset:48px;z-index:99999;pointer-events:none;background:#2244aa';
      document.body.append(animation);
      let frame = 0;
      const interval = setInterval(() => {
        animation.style.background = frame++ % 2 ? '#2244aa' : '#dd7722';
      }, 100);
      Object.defineProperty(window, '__gulNativeAnimation', {
        value: () => {
          clearInterval(interval);
          animation.remove();
        },
      });
    });
    await apps[0].evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].focus());
    await expect
      .poll(
        async () => {
          const measurement = await remoteStereo(viewer);
          return measurement.peak > 0.02 && measurement.separation > 12;
        },
        { timeout: 15_000 },
      )
      .toBe(true);
    await expect
      .poll(
        () =>
          viewer.locator('video').evaluateAll((elements) =>
            elements.some((element) => {
              const video = element as HTMLVideoElement;
              return (
                video.videoWidth > 0 &&
                video.videoWidth <= 1280 &&
                video.videoHeight > 0 &&
                video.videoHeight <= 720 &&
                video.getVideoPlaybackQuality().totalVideoFrames > 15
              );
            }),
          ),
        { timeout: 15_000 },
      )
      .toBe(true);
    await expect.poll(() => movingDesktop(viewer), { timeout: 15_000 }).toBe(true);
    for (const page of pages) await expect.poll(() => selectedRelay(page)).toBe(true);
    // The receiver is deafened only at playback. Raw decoded PCM above proves capture and delivery.
    await publisher.evaluate(() =>
      (window as unknown as { __gulNativeAnimation: () => void }).__gulNativeAnimation(),
    );
    await publisher.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
    await expect(viewer.locator('video')).toHaveCount(0);
    await expect(viewer.locator('audio[data-source="screen"]')).toHaveCount(0);
  } finally {
    player?.kill();
    await Promise.all(apps.map((app) => app.close().catch(() => {})));
    if (oldSink) await pulse('set-default-sink', oldSink).catch(() => {});
    if (module) await pulse('unload-module', module).catch(() => {});
    await rm(dataRoot, { recursive: true, force: true });
  }
});
