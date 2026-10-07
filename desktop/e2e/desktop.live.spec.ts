import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import { readFile, mkdtemp, rm, access } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { privateLoginForm } from './login-form.ts';
import {
  isolatedGameAudio,
  mediaTestArguments,
  nativeDisplayTestEnvironment,
} from './linux-audio-fixture.ts';
import { installMicrophoneCalibration } from './microphone-calibration.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
test.skip(!fixture, 'Start the isolated REALITY fixture and set GUL_ELECTRON_STAND_DIR.');

async function synthetic(page: Page) {
  await page.evaluate(installMicrophoneCalibration, process.platform === 'linux');
  await page.evaluate((linux) => {
    const nativeDisplay = navigator.mediaDevices.getDisplayMedia.bind(navigator.mediaDevices);
    const peers: RTCPeerConnection[] = [];
    const captures: MediaStream[] = [];
    const playbackMeters: AnalyserNode[] = [];
    const connect = AudioNode.prototype.connect;
    AudioNode.prototype.connect = function (this: AudioNode, ...args: unknown[]) {
      const result = Reflect.apply(connect, this, args);
      if (this instanceof GainNode && args[0] instanceof AudioDestinationNode) {
        const analyser = this.context.createAnalyser();
        analyser.fftSize = 1024;
        Reflect.apply(connect, this, [analyser]);
        playbackMeters.push(analyser);
      }
      return result;
    } as AudioNode['connect'];
    const Original = window.RTCPeerConnection;
    Object.defineProperty(window, '__gulTestPeers', { value: peers });
    Object.defineProperty(window, '__gulTestCaptures', { value: captures });
    Object.defineProperty(window, '__gulTestPlaybackMeters', { value: playbackMeters });
    window.RTCPeerConnection = class extends Original {
      constructor(config?: RTCConfiguration) {
        super(config);
        peers.push(this);
      }
    };
    const audio = () => {
      const context = new AudioContext({ sampleRate: 48000 });
      const destination = context.createMediaStreamDestination();
      const merger = context.createChannelMerger(2);
      [440, 880].forEach((frequency, channel) => {
        const oscillator = context.createOscillator();
        const gain = context.createGain();
        oscillator.frequency.value = frequency;
        gain.gain.value = 0.1;
        oscillator.connect(gain).connect(merger, 0, channel);
        oscillator.start();
      });
      merger.connect(destination);
      void context.resume();
      const track = destination.stream.getAudioTracks()[0];
      const stop = track.stop.bind(track);
      track.stop = () => {
        stop();
        void context.close();
      };
      return destination.stream;
    };
    navigator.mediaDevices.getDisplayMedia = async (options) => {
      // Called during the real button gesture: production main still grants source consent.
      const native = linux ? await nativeDisplay(options) : undefined;
      const canvas = document.createElement('canvas');
      canvas.width = 1280;
      canvas.height = 720;
      const context = canvas.getContext('2d')!;
      let frame = 0;
      const timer = setInterval(() => {
        context.fillStyle = `hsl(${(frame++ * 5) % 360} 60% 40%)`;
        context.fillRect(0, 0, 1280, 720);
        context.fillStyle = '#ffffff';
        context.fillRect((frame * 10) % 1200, 200, 80, 80);
      }, 33);
      const stream = canvas.captureStream(30);
      if (!linux) stream.addTrack(audio().getAudioTracks()[0]);
      const track = stream.getVideoTracks()[0];
      const stop = track.stop.bind(track);
      track.stop = () => {
        clearInterval(timer);
        stop();
        native?.getTracks().forEach((track) => track.stop());
      };
      captures.push(stream);
      return stream;
    };
  }, process.platform === 'linux');
}

async function audible(page: Page, kind: string) {
  return page.evaluate(async (kind) => {
    const element = document.querySelector<HTMLAudioElement>(`audio[data-source="${kind}"]`);
    if (!element?.srcObject) return 0;
    const context = new AudioContext();
    const source = context.createMediaStreamSource(element.srcObject as MediaStream);
    const analyser = context.createAnalyser();
    analyser.fftSize = 2048;
    source.connect(analyser);
    const values = new Float32Array(analyser.fftSize);
    let peak = 0;
    await context.resume();
    for (let i = 0; i < 12; i++) {
      await new Promise((r) => setTimeout(r, 50));
      analyser.getFloatTimeDomainData(values);
      peak = Math.max(peak, ...values.map(Math.abs));
    }
    source.disconnect();
    await context.close();
    return peak;
  }, kind);
}

async function playbackPeak(page: Page) {
  return page.evaluate(async () => {
    const meters = (window as unknown as { __gulTestPlaybackMeters: AnalyserNode[] }).__gulTestPlaybackMeters;
    let peak = 0;
    for (let i = 0; i < 6; i++) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      for (const meter of meters) {
        if (meter.context.state !== 'running') continue;
        const samples = new Float32Array(meter.fftSize);
        meter.getFloatTimeDomainData(samples);
        peak = Math.max(peak, ...samples.map(Math.abs));
      }
    }
    return peak;
  });
}

async function stereoSeparation(page: Page) {
  return page.evaluate(async () => {
    const element = document.querySelector<HTMLAudioElement>('audio[data-source="screen"]');
    if (!element?.srcObject) return -100;
    const context = new AudioContext({ sampleRate: 48000 });
    const source = context.createMediaStreamSource(element.srcObject as MediaStream);
    const splitter = context.createChannelSplitter(2);
    const analysers = [context.createAnalyser(), context.createAnalyser()];
    source.connect(splitter);
    analysers.forEach((analyser, channel) => {
      analyser.fftSize = 4096;
      splitter.connect(analyser, channel);
    });
    await context.resume();
    await new Promise((resolve) => setTimeout(resolve, 400));
    const energies = analysers.map((analyser) => {
      const values = new Float32Array(analyser.frequencyBinCount);
      analyser.getFloatFrequencyData(values);
      return [440, 880].map((frequency) => {
        const bin = Math.round((frequency * analyser.fftSize) / context.sampleRate);
        return Math.max(...values.slice(bin - 2, bin + 3));
      });
    });
    source.disconnect();
    await context.close();
    const separation = Math.min(energies[0][0] - energies[0][1], energies[1][1] - energies[1][0]);
    return Number.isFinite(separation) ? separation : -100;
  });
}

async function movingImage(page: Page) {
  return page.locator('video').evaluate(async (element) => {
    const video = element as HTMLVideoElement;
    const canvas = document.createElement('canvas');
    canvas.width = 1;
    canvas.height = 1;
    const context = canvas.getContext('2d')!;
    const sample = () => {
      context.drawImage(video, 20, 20, 1, 1, 0, 0, 1, 1);
      return [...context.getImageData(0, 0, 1, 1).data];
    };
    const before = sample();
    await new Promise((resolve) => setTimeout(resolve, 250));
    return sample()
      .slice(0, 3)
      .some((value, index) => Math.abs(value - before[index]) > 10);
  });
}

test('two Electron clients exchange voice, chat and moving stereo screen through REALITY', async () => {
  const directory = resolve(fixture!);
  const address = (await readFile(join(directory, 'address'), 'utf8')).trim();
  const password = (await readFile(join(directory, 'join-password'), 'utf8')).trim();
  const caFile = join(directory, 'ca.pem');
  const fixtureCA = await access(caFile)
    .then(() => caFile)
    .catch(() => undefined);
  const apps: ElectronApplication[] = [];
  const dataRoot = await mkdtemp(join(tmpdir(), 'gul-electron-e2e-'));
  let stopGameAudio = async () => {};
  try {
    const gameAudio = await isolatedGameAudio();
    stopGameAudio = gameAudio.close;
    const pages: Page[] = [];
    for (let i = 0; i < 2; i++) {
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
          ...(fixtureCA ? { GUL_ELECTRON_TEST_CA: fixtureCA } : {}),
        },
      });
      apps.push(app);
      const page = await app.firstWindow();
      pages.push(page);
      await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
      await synthetic(page);
      await privateLoginForm(page, address, password);
      await page.getByLabel('Твой ник', { exact: true }).fill(`desktop-peer-${i}`);
      await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    }
    const [a, b] = pages;
    await expect.poll(() => audible(b, 'voice'), { timeout: 15_000 }).toBeGreaterThan(0.005);
    await expect.poll(() => audible(a, 'voice'), { timeout: 15_000 }).toBeGreaterThan(0.005);
    await expect.poll(() => playbackPeak(a)).toBeGreaterThan(0.005);
    await expect.poll(() => playbackPeak(b)).toBeGreaterThan(0.005);
    await a.getByRole('button', { name: 'Настройки', exact: true }).click();
    await expect(a.getByRole('meter', { name: 'Уровень микрофона', exact: true })).toBeVisible();
    await a.getByRole('combobox', { name: 'Режим микрофона', exact: true }).selectOption('vad');
    const threshold = a.getByRole('slider', { name: 'Порог активации', exact: true });
    await threshold.focus();
    await a.keyboard.press('End');
    await expect(threshold).toHaveValue('-6');
    await expect.poll(() => audible(b, 'voice')).toBeLessThan(0.005);
    await a.getByRole('combobox', { name: 'Режим микрофона', exact: true }).selectOption('continuous');
    await expect.poll(() => audible(b, 'voice')).toBeGreaterThan(0.005);
    await a.keyboard.press('Escape');
    await b
      .locator('.members')
      .getByRole('button', { name: 'Настройки участника desktop-peer-0', exact: true })
      .click();
    const volume = b.getByRole('slider', { name: 'Громкость desktop-peer-0', exact: true });
    await volume.focus();
    await b.keyboard.press('End');
    for (let step = 0; step < 10; step++) await b.keyboard.press('ArrowLeft');
    await expect(volume).toHaveValue('1.5');
    await b.getByRole('checkbox', { name: 'Выключить участника desktop-peer-0', exact: true }).check();
    await expect.poll(() => playbackPeak(b)).toBeLessThan(0.005);
    await b.getByRole('checkbox', { name: 'Выключить участника desktop-peer-0', exact: true }).uncheck();
    await expect(volume).toHaveValue('1.5');
    await expect.poll(() => playbackPeak(b)).toBeGreaterThan(0.005);
    await b.keyboard.press('Escape');
    await a.getByRole('button', { name: 'Выключить микрофон', exact: true }).click();
    await expect.poll(() => audible(b, 'voice')).toBeLessThan(0.005);
    await a.getByRole('button', { name: 'Включить микрофон', exact: true }).click();
    await expect.poll(() => audible(b, 'voice')).toBeGreaterThan(0.005);
    await b.getByRole('button', { name: 'Выключить звук', exact: true }).click();
    await expect.poll(() => playbackPeak(b)).toBeLessThan(0.005);
    await b.getByRole('button', { name: 'Включить звук', exact: true }).click();
    await expect.poll(() => playbackPeak(b)).toBeGreaterThan(0.005);
    await a.getByLabel('Сообщение', { exact: true }).fill('REALITY desktop integration');
    await a.getByRole('button', { name: 'Отправить сообщение' }).click();
    await expect(b.getByText('REALITY desktop integration', { exact: true })).toBeVisible();
    await a.getByRole('button', { name: 'Показать экран', exact: true }).click();
    await expect(b.getByRole('button', { name: /desktop-peer-0.*Смотреть экран/ })).toBeVisible({
      timeout: 20_000,
    });
    await b.getByRole('button', { name: /desktop-peer-0.*Смотреть экран/ }).click();
    await expect
      .poll(
        () =>
          b
            .locator('video')
            .evaluateAll((elements) =>
              elements.some((e) => (e as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames > 15),
            ),
        { timeout: 20_000 },
      )
      .toBe(true);
    await expect.poll(() => audible(b, 'screen'), { timeout: 15_000 }).toBeGreaterThan(0.005);
    await expect.poll(() => stereoSeparation(b)).toBeGreaterThan(12);
    await expect.poll(() => movingImage(b)).toBe(true);
    if (process.platform === 'darwin') {
      await apps[1].evaluate(({ BrowserWindow }) => {
        const state = globalThis as typeof globalThis & { __gulFullscreenEntered?: boolean };
        state.__gulFullscreenEntered = false;
        BrowserWindow.getAllWindows()[0].once('enter-full-screen', () => {
          state.__gulFullscreenEntered = true;
        });
      });
    }
    await b.getByRole('button', { name: 'На весь экран', exact: true }).click();
    await expect.poll(() => b.evaluate(() => Boolean(document.fullscreenElement))).toBe(true);
    if (process.platform === 'darwin') {
      await expect
        .poll(() =>
          apps[1].evaluate(() =>
            Boolean(
              (globalThis as typeof globalThis & { __gulFullscreenEntered?: boolean }).__gulFullscreenEntered,
            ),
          ),
        )
        .toBe(true);
    }
    await b.bringToFront();
    await b.keyboard.press('Escape');
    await expect.poll(() => b.evaluate(() => Boolean(document.fullscreenElement))).toBe(false);
    await expect
      .poll(() =>
        b.evaluate(() => {
          const message = document.querySelector('.chat-message')?.getBoundingClientRect();
          const viewport = document.querySelector('.chat')?.getBoundingClientRect();
          return Boolean(
            message && viewport && message.top >= viewport.top && message.bottom <= viewport.bottom,
          );
        }),
      )
      .toBe(true);
    for (const page of pages) {
      const relays = await page.evaluate(async () => {
        const peers = (window as unknown as { __gulTestPeers: RTCPeerConnection[] }).__gulTestPeers;
        const selected: { type: string; protocol: string }[] = [];
        for (const peer of peers) {
          if (peer.connectionState !== 'connected') continue;
          const stats = await peer.getStats();
          stats.forEach((s) => {
            if (s.type === 'transport' && s.selectedCandidatePairId) {
              const pair = stats.get(s.selectedCandidatePairId);
              const local = stats.get(pair.localCandidateId);
              selected.push({ type: local.candidateType, protocol: local.relayProtocol });
            }
          });
        }
        return selected;
      });
      expect(relays.length).toBeGreaterThan(0);
      expect(relays.every((r) => r.type === 'relay' && r.protocol === 'tcp')).toBe(true);
    }
    await b.screenshot({ path: 'test-results/desktop-connected.png' });
    await a.getByRole('button', { name: 'Остановить демонстрацию' }).click();
    await expect(b.locator('video')).toHaveCount(0);
    for (let cycle = 0; cycle < 20; cycle++) {
      console.log(`Screen lifecycle cycle ${cycle + 1}/20`);
      await a.getByRole('button', { name: 'Показать экран', exact: true }).click();
      const watch = b.getByRole('button', { name: /desktop-peer-0.*Смотреть экран/ });
      await expect(watch).toBeVisible();
      await watch.click();
      await expect
        .poll(() =>
          b
            .locator('video')
            .evaluateAll((elements) =>
              elements.some((e) => (e as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames > 2),
            ),
        )
        .toBe(true);
      if (cycle % 5 === 0) {
        await a.getByRole('button', { name: 'Игра', exact: true }).click();
        await expect(b.locator('video')).toHaveCount(0);
        await expect(a.getByText('Голос подключён', { exact: true })).toBeVisible();
        await a.getByRole('button', { name: 'Общая', exact: true }).click();
        await expect.poll(() => audible(b, 'voice')).toBeGreaterThan(0.005);
        await expect(a.getByText('REALITY desktop integration', { exact: true })).toBeVisible();
      } else {
        await a.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
        await expect(b.locator('video')).toHaveCount(0);
      }
      await expect
        .poll(() =>
          a.evaluate(() =>
            (window as unknown as { __gulTestCaptures: MediaStream[] }).__gulTestCaptures.every((stream) =>
              stream.getTracks().every((track) => track.readyState === 'ended'),
            ),
          ),
        )
        .toBe(true);
      for (const page of pages) {
        await expect
          .poll(() =>
            page.evaluate(
              () =>
                (window as unknown as { __gulTestPeers: RTCPeerConnection[] }).__gulTestPeers.filter(
                  (peer) => peer.connectionState !== 'closed',
                ).length,
            ),
          )
          .toBeLessThanOrEqual(2);
      }
    }
    await a.getByRole('button', { name: 'Отключиться' }).click();
    await expect(a.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    await apps[0].close();
    apps.shift();
    const restored = await electron.launch({
      executablePath: require('electron'),
      args: ['.', '--gul-electron-test', `--user-data-dir=${join(dataRoot, '0')}`],
      env: { ...process.env, NODE_ENV: 'test', ...(fixtureCA ? { GUL_ELECTRON_TEST_CA: fixtureCA } : {}) },
    });
    apps.push(restored);
    const restoredPage = await restored.firstWindow();
    await expect(restoredPage.getByLabel('Твой ник', { exact: true })).toHaveValue('desktop-peer-0');
    await expect(restoredPage.getByLabel('Адрес сервера', { exact: true })).toHaveValue(address);
    await expect(restoredPage.getByLabel('Пароль', { exact: true })).toHaveValue('');
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
