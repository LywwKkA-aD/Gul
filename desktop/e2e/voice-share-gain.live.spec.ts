import {
  test,
  expect,
  _electron as electron,
  type ElectronApplication,
  type Locator,
  type Page,
} from '@playwright/test';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { execFile, spawn, type ChildProcess } from 'node:child_process';
import { chmod, lstat, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { privateLoginForm } from './login-form.ts';
import { startScreen } from './start-screen.ts';
import {
  isolatedGameAudio,
  nativeDisplayTestEnvironment,
  stopFixtureProcess,
} from './linux-audio-fixture.ts';
import {
  installVoiceShareGainProbe,
  installShareGainScreenFixture,
  type ShareGainSample,
  type ShareGainState,
  type VoiceShareGainProbe,
} from './voice-share-gain-probe.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
const inputGain = 1.2;
// Cover at least three complete loops of both independent public utterances.
const sampleDuration = 18_000;
const warmupDuration = 12_000;
const name = (peer: number) => `share-gain-peer-${peer}`;

async function privatePulseCommand(
  environment: Readonly<Record<string, string>>,
  stage: 'playback-sink' | 'microphone-source' | 'default-output' | 'default-input' | 'input-volume',
  args: readonly string[],
) {
  await new Promise<void>((done, fail) => {
    execFile('pactl', [...args], { env: { ...process.env, ...environment }, timeout: 3000 }, (error) =>
      error ? fail(new Error(`Private gain microphone unavailable (${stage}).`)) : done(),
    );
  });
}

/** The microphone is a real Pulse pipe source, not a foreign playback stream that
 * native screen capture could accidentally include. Only the game has a foreign sink input.
 */
async function pulseMicrophone(
  environment: Readonly<Record<string, string>>,
  peer: number,
  speech: string,
  directory: string,
): Promise<ChildProcess> {
  const endpoint = environment.PULSE_SERVER;
  if (!endpoint?.startsWith('unix:')) throw new Error('Private gain audio server unavailable.');
  const playback = `gul_gain_output_${peer}`;
  const pipe = join(directory, `microphone-${peer}.pipe`);
  await privatePulseCommand(environment, 'playback-sink', [
    '--server',
    endpoint,
    'load-module',
    'module-null-sink',
    `sink_name=${playback}`,
    'rate=48000',
    'channels=2',
  ]);
  // PulseAudio 16.1 creates the FIFO itself and rejects a pre-existing mkfifo.
  // The parent directory is private; restrict its new FIFO before opening the writer.
  await privatePulseCommand(environment, 'microphone-source', [
    '--server',
    endpoint,
    'load-module',
    'module-pipe-source',
    `file=${pipe}`,
    `source_name=gul_gain_mic_${peer}`,
    'rate=48000',
    'format=s16le',
    'channels=1',
    'channel_map=mono',
    `source_properties=device.description=Gul-Gain-Microphone-${peer}`,
  ]);
  if (!(await lstat(pipe)).isFIFO()) throw new Error('Private gain pipe unavailable (FIFO type).');
  await chmod(pipe, 0o600);
  await privatePulseCommand(environment, 'default-output', [
    '--server',
    endpoint,
    'set-default-sink',
    playback,
  ]);
  await privatePulseCommand(environment, 'default-input', [
    '--server',
    endpoint,
    'set-default-source',
    `gul_gain_mic_${peer}`,
  ]);
  await privatePulseCommand(environment, 'input-volume', [
    '--server',
    endpoint,
    'set-source-volume',
    `gul_gain_mic_${peer}`,
    // Actual private-source PCM measured -33.38 dBFS RMS at 70%; Pulse's cubic
    // 20% volume attenuated this public speech to an unrealistic -66.02 dBFS.
    '70%',
  ]);
  // A pipe source consumes PCM as fast as supplied. Pace 480 mono frames per 10ms,
  // reusing one chunk without a catch-up queue. This never creates a sink input
  // or replaces a browser MediaStreamTrack.
  const writer = `
    const fs = require('node:fs');
    const wav = fs.readFileSync(process.argv[1]);
    let pcm;
    for (let offset = 12; offset + 8 <= wav.length;) {
      const size = wav.readUInt32LE(offset + 4);
      if (size > wav.length - offset - 8) process.exit(2);
      if (wav.toString('ascii', offset, offset + 4) === 'data') pcm = wav.subarray(offset + 8, offset + 8 + size);
      offset += 8 + size + (size % 2);
    }
    if (!pcm || !pcm.length) process.exit(2);
    const file = fs.openSync(process.argv[2], 'w');
    const chunk = Buffer.alloc(480 * 2);
    let cursor = 0;
    setInterval(() => {
      let filled = 0;
      while (filled < chunk.length) {
        const count = Math.min(pcm.length - cursor, chunk.length - filled);
        pcm.copy(chunk, filled, cursor, cursor + count);
        filled += count;
        cursor = (cursor + count) % pcm.length;
      }
      let written = 0;
      while (written < chunk.length) written += fs.writeSync(file, chunk, written, chunk.length - written);
    }, 10);
  `;
  const child = spawn(process.execPath, ['-e', writer, speech, pipe], {
    stdio: 'ignore',
    shell: false,
    env: { ...process.env, ...environment },
  });
  await new Promise<void>((done, fail) => {
    child.once('spawn', done);
    child.once('error', () => fail(new Error('Private gain speech unavailable.')));
  });
  return child;
}

async function sourceVolume(environment: Readonly<Record<string, string>>, peer: number): Promise<number[]> {
  if (process.platform !== 'linux') return [];
  return new Promise((done, fail) => {
    execFile(
      'pactl',
      ['--server', environment.PULSE_SERVER, '--format=json', 'list', 'sources'],
      { env: { ...process.env, ...environment }, timeout: 3000, maxBuffer: 256 * 1024 },
      (error, output) => {
        try {
          if (error) throw new Error();
          const sources: { name: string; volume: Record<string, { value: number }> }[] = JSON.parse(output);
          const source = sources.find((candidate) => candidate.name === `gul_gain_mic_${peer}`);
          const volume = Object.values(source?.volume ?? {}).map((channel) => channel.value);
          if (!volume.length || volume.some((value) => !Number.isFinite(value))) throw new Error();
          done(volume);
        } catch {
          fail(new Error('Private gain volume unavailable.'));
        }
      },
    );
  });
}

async function configureMicrophone(page: Page, peer: number) {
  await page.getByRole('button', { name: 'Настройки', exact: true }).click();
  if (process.platform === 'linux') {
    const selector = page.getByRole('combobox', { name: 'Устройство микрофона', exact: true });
    await selector.selectOption({ label: `Gul-Gain-Microphone-${peer}` });
    await expect(selector).toBeEnabled();
  }
  const mode = page.getByRole('combobox', { name: 'Режим микрофона', exact: true });
  await expect(mode).toBeEnabled();
  await mode.selectOption('continuous');
  const gain = page.getByRole('slider', { name: 'Усиление микрофона', exact: true });
  await setRange(gain, inputGain);
  await expect(page.getByRole('checkbox', { name: 'Автоматическая громкость', exact: true })).toBeChecked();
  await expect(page.getByRole('checkbox', { name: 'Убирать эхо', exact: true })).toBeChecked();
  await expect(page.getByRole('checkbox', { name: 'Шумоподавление', exact: true })).toBeChecked();
  await page.keyboard.press('Escape');
}

/** Device and mode changes can temporarily disable the settings form. Acknowledge
 * every intended slider step before the next key, including the async microphone settings.
 */
async function setRange(slider: Locator, value: number) {
  await expect(slider).toBeEnabled();
  const start = Math.round(Number(await slider.inputValue()) / 0.05);
  const end = Math.round(value / 0.05);
  const direction = end > start ? 1 : -1;
  for (let step = start; step !== end;) {
    step += direction;
    await expect(slider).toBeEnabled();
    await slider.press(direction > 0 ? 'ArrowRight' : 'ArrowLeft');
    await expect(slider).toHaveValue(String(Number((step * 0.05).toFixed(2))));
    await expect(slider).toBeEnabled();
  }
  await expect(slider).toHaveValue(String(value));
}

async function configurePeerVolume(page: Page, remote: number, gain: number) {
  await page
    .locator('.members')
    .getByRole('button', { name: `Настройки участника ${name(remote)}`, exact: true })
    .click();
  const slider = page.getByRole('slider', { name: `Громкость ${name(remote)}`, exact: true });
  await setRange(slider, gain);
  await page.keyboard.press('Escape');
}

function stableState(actual: ShareGainState, baseline: ShareGainState, gain: number, screens: number) {
  expect(actual).toMatchObject({
    sameCapture: true,
    sameConstraints: true,
    sameSettings: true,
    rawGraphs: baseline.rawGraphs,
    outgoingGraphs: baseline.outgoingGraphs,
    voiceGainGraphs: baseline.voiceGainGraphs,
    inputGain,
    voiceElements: 1,
    screenElements: screens,
    silentVoiceElements: true,
    rawFlags: {
      echoCancellation: true,
      autoGainControl: true,
      noiseSuppression: false,
      sampleRate: 48000,
      channelCount: 1,
    },
  });
  expect(actual.gainValues).toHaveLength(1);
  expect(actual.gainValues[0]).toBeCloseTo(gain, 3);
}

function stableLevels(actual: ShareGainSample, baseline: ShareGainSample, gain: number) {
  for (const stage of ['raw', 'outgoing', 'decoded', 'output'] as const) {
    expect(actual.levels[stage].frames).toBeGreaterThan(100);
    expect(actual.levels[stage].speechDb, `${stage} p90 RMS dB`).toBeGreaterThan(-50);
    expect(
      Math.abs(actual.levels[stage].speechDb - baseline.levels[stage].speechDb),
      `${stage} p90 RMS drift`,
    ).toBeLessThan(2);
    expect(
      Math.abs(actual.levels[stage].voicedDb - baseline.levels[stage].voicedDb),
      `${stage} voiced median drift`,
    ).toBeLessThan(2.5);
  }
  expect(actual.measuredOutputGainDb).toBeCloseTo(20 * Math.log10(gain), 0);
  const pipeline = (sample: ShareGainSample) => sample.levels.outgoing.speechDb - sample.levels.raw.speechDb;
  expect(Math.abs(pipeline(actual) - pipeline(baseline)), 'microphone processor gain drift').toBeLessThan(
    1.5,
  );
}

async function sample(pages: readonly Page[]): Promise<ShareGainSample[]> {
  return Promise.all(
    pages.map((page) =>
      page.evaluate(
        (duration) =>
          (
            window as unknown as { __gulVoiceShareGainProbe: VoiceShareGainProbe }
          ).__gulVoiceShareGainProbe.sample(duration),
        sampleDuration,
      ),
    ),
  );
}

async function settleBeforeShares(pages: readonly Page[], checkpoints: readonly ShareGainState[]) {
  let previous: readonly ShareGainSample[] | undefined;
  let stableWindows = 0;
  for (let window = 1; window <= 4; window++) {
    const current = await sample(pages);
    current.forEach((measurement, peer) => {
      stableState(measurement.state, checkpoints[peer], 0.65, 0);
      stableLevels(measurement, measurement, 0.65);
    });
    const drift = current.flatMap((measurement, peer) =>
      (['raw', 'outgoing', 'decoded', 'output'] as const).flatMap((stage) => {
        const prior = previous?.[peer].levels[stage];
        return prior
          ? [
              Math.abs(measurement.levels[stage].speechDb - prior.speechDb),
              Math.abs(measurement.levels[stage].voicedDb - prior.voicedDb),
            ]
          : [];
      }),
    );
    const maximumDrift = drift.length ? Math.max(...drift) : undefined;
    stableWindows = maximumDrift !== undefined && maximumDrift <= 1.5 ? stableWindows + 1 : 0;
    console.info(
      'GUL_SHARE_GAIN_PREROLL',
      JSON.stringify({ window, maximumDrift, stableWindows, levels: current.map((value) => value.levels) }),
    );
    if (stableWindows >= 2) return;
    previous = current;
  }
  throw new Error('Microphone APM did not settle before screen sharing (two stable control windows).');
}

test('two encoded REALITY peers preserve microphone and speaker gain through two own screen shares', async () => {
  test.skip(
    process.platform === 'win32',
    'This case requires isolated Pulse audio or macOS synthetic display audio; Windows native capture needs a dedicated game fixture.',
  );
  test.skip(!fixture, 'Set GUL_ELECTRON_STAND_DIR to the pinned isolated REALITY fixture.');
  const directory = await mkdtemp(join(tmpdir(), 'gul-share-gain-'));
  const apps: ElectronApplication[] = [];
  const pages: Page[] = [];
  const inputs: ChildProcess[] = [];
  const originalVolumes: number[][] = [];
  let closeAudio = async () => {};
  try {
    const hashes = new Map(
      (await readFile(new URL('./testdata/noise/SHA256SUMS', import.meta.url), 'utf8'))
        .trim()
        .split('\n')
        .map((line) => {
          const [hash, file] = line.trim().split(/\s+/);
          return [file, hash];
        }),
    );
    const microphones = await Promise.all(
      ['clean-speech.wav', 'clean-speech-peer.wav'].map(async (file, peer) => {
        const microphone = join(directory, `microphone-${peer}.wav`);
        const speech = await readFile(new URL(`./testdata/noise/${file}`, import.meta.url));
        expect(createHash('sha256').update(speech).digest('hex'), 'Pinned public speech PCM').toBe(
          hashes.get(file),
        );
        await writeFile(microphone, speech, { mode: 0o600 });
        return microphone;
      }),
    );
    const address = (await readFile(resolve(fixture!, 'address'), 'utf8')).trim();
    const password = (await readFile(resolve(fixture!, 'join-password'), 'utf8')).trim();
    const audio = await isolatedGameAudio();
    closeAudio = audio.close;
    for (let peer = 0; peer < 2; peer++) {
      const microphone = microphones[peer];
      if (process.platform === 'linux')
        inputs.push(await pulseMicrophone(audio.environments[peer], peer, microphone, directory));
      originalVolumes.push(await sourceVolume(audio.environments[peer], peer));
      originalVolumes[peer].forEach((value) => expect(Math.abs(value - 45875)).toBeLessThanOrEqual(1));
      const app = await electron.launch({
        executablePath: require('electron'),
        args: [
          '.',
          '--gul-electron-test',
          ...(process.platform === 'linux'
            ? []
            : [
                '--use-fake-device-for-media-stream',
                `--use-file-for-fake-audio-capture=${microphone}`,
                '--disable-features=AudioServiceSandbox',
                '--mute-audio',
              ]),
          `--user-data-dir=${join(directory, String(peer))}`,
        ],
        env: {
          ...process.env,
          NODE_ENV: 'test',
          ...nativeDisplayTestEnvironment,
          ...audio.environments[peer],
          GUL_ELECTRON_TEST_CA: resolve(fixture!, 'ca.pem'),
        },
      });
      apps.push(app);
      const analogAdjustmentDisabled = await app.evaluate(({ app }) =>
        app.commandLine
          .getSwitchValue('disable-features')
          .split(',')
          .includes('WebRtcAllowInputVolumeAdjustment'),
      );
      expect(
        analogAdjustmentDisabled,
        'Chromium digital AGC must not change the shared microphone volume',
      ).toBe(true);
      const page = await app.firstWindow();
      pages.push(page);
      await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
      await page.evaluate(installVoiceShareGainProbe);
      await page.evaluate(installShareGainScreenFixture, process.platform === 'linux');
      await privateLoginForm(page, address, password);
      await page.getByLabel('Твой ник', { exact: true }).fill(name(peer));
      await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
      await configureMicrophone(page, peer);
    }
    for (const page of pages) await expect(page.locator('audio[data-source="voice"]')).toHaveCount(1);
    await Promise.all(pages.map((page, peer) => configurePeerVolume(page, 1 - peer, 0.65)));
    // Independent speech avoids identical microphone/playback waveforms biasing AEC.
    // Confirm bounded cold-start settling before sharing; transitions retain this capture.
    await new Promise((done) => setTimeout(done, warmupDuration));
    const checkpoints = await Promise.all(
      pages.map((page) =>
        page.evaluate(() =>
          (
            window as unknown as { __gulVoiceShareGainProbe: VoiceShareGainProbe }
          ).__gulVoiceShareGainProbe.checkpoint(),
        ),
      ),
    );
    await settleBeforeShares(pages, checkpoints);
    const [publisher, viewer] = pages;
    for (const [cycle, gain] of [0.65, 1.4].entries()) {
      await Promise.all(pages.map((page, peer) => configurePeerVolume(page, 1 - peer, gain)));
      const before = await sample(pages);
      console.info('GUL_SHARE_GAIN_BASELINE', JSON.stringify({ cycle: cycle + 1, gain, inputGain, before }));
      const beforeVolumes = await Promise.all(audio.environments.map(sourceVolume));
      expect(beforeVolumes, 'Microphone capture must preserve private system source volumes').toEqual(
        originalVolumes,
      );
      before.forEach((measurement, peer) => {
        stableState(measurement.state, checkpoints[peer], gain, 0);
        stableLevels(measurement, measurement, gain);
      });
      await startScreen(publisher, '720p30');
      const watch = viewer.getByRole('button', { name: new RegExp(`${name(0)}.*Смотреть экран`) });
      await expect(watch).toBeVisible({ timeout: 20_000 });
      await watch.click();
      await expect(viewer.locator('audio[data-source="screen"]')).toHaveCount(1, { timeout: 20_000 });
      await expect(viewer.locator('.screen-viewer video')).toBeVisible();
      await expect
        .poll(
          () =>
            viewer.evaluate(() =>
              (
                window as unknown as { __gulVoiceShareGainProbe: VoiceShareGainProbe }
              ).__gulVoiceShareGainProbe.screenReady(),
            ),
          { timeout: 10_000, message: 'Encoded screen audio must contain audible stereo markers' },
        )
        .toBe(true);
      const sharing = await sample(pages);
      console.info('GUL_SHARE_GAIN_SHARING', JSON.stringify({ cycle: cycle + 1, gain, sharing }));
      const sharingVolumes = await Promise.all(audio.environments.map(sourceVolume));
      expect(sharingVolumes, 'Screen startup must preserve private system source volumes').toEqual(
        originalVolumes,
      );
      sharing.forEach((measurement, peer) => {
        stableState(measurement.state, checkpoints[peer], gain, peer === 1 ? 1 : 0);
        stableLevels(measurement, before[peer], gain);
      });
      expect(sharing[1].stereo?.validFrames).toBeGreaterThan(100);
      expect(sharing[1].stereo!.validFrames / sharing[1].stereo!.frames).toBeGreaterThan(0.9);
      expect(sharing[1].stereo?.leftMarkerDb).toBeGreaterThan(-60);
      expect(sharing[1].stereo?.rightMarkerDb).toBeGreaterThan(-60);
      expect(sharing[1].stereo?.leftSeparationDb).toBeGreaterThan(8);
      expect(sharing[1].stereo?.rightSeparationDb).toBeGreaterThan(8);
      // The encoded marker fixture measured -58 to -62 dB outside marker bands.
      // A -30 dB ceiling leaves codec headroom while rejecting a broadband voice mix.
      expect(sharing[1].stereo?.leftNonMarkerDb).toBeLessThan(-30);
      expect(sharing[1].stereo?.rightNonMarkerDb).toBeLessThan(-30);
      await publisher.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click();
      await expect(viewer.locator('audio[data-source="screen"]')).toHaveCount(0);
      await expect(viewer.locator('.screen-viewer video')).toHaveCount(0);
      const stopped = await sample(pages);
      const stoppedVolumes = await Promise.all(audio.environments.map(sourceVolume));
      expect(stoppedVolumes, 'Screen teardown must preserve private system source volumes').toEqual(
        originalVolumes,
      );
      stopped.forEach((measurement, peer) => {
        stableState(measurement.state, checkpoints[peer], gain, 0);
        stableLevels(measurement, before[peer], gain);
      });
      console.info(
        'GUL_SHARE_GAIN',
        JSON.stringify({
          cycle: cycle + 1,
          gain,
          inputGain,
          before,
          sharing,
          stopped,
          sourceVolumes: { before: beforeVolumes, sharing: sharingVolumes, stopped: stoppedVolumes },
        }),
      );
    }
    for (const page of pages) {
      const relay = await page.evaluate(() =>
        (
          window as unknown as { __gulVoiceShareGainProbe: VoiceShareGainProbe }
        ).__gulVoiceShareGainProbe.relay(),
      );
      expect(relay.length).toBeGreaterThan(0);
      expect(relay.every((candidate) => candidate.type === 'relay' && candidate.protocol === 'tcp')).toBe(
        true,
      );
    }
  } finally {
    await Promise.allSettled(
      pages.map((page) =>
        page.evaluate(() =>
          (
            window as unknown as { __gulVoiceShareGainProbe: VoiceShareGainProbe }
          ).__gulVoiceShareGainProbe.close(),
        ),
      ),
    );
    await Promise.allSettled(apps.map((app) => app.close()));
    await Promise.allSettled(inputs.map(stopFixtureProcess));
    await closeAudio();
    await rm(directory, { recursive: true, force: true });
  }
});
