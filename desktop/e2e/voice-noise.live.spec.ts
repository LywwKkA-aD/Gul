import { startScreen } from './start-screen.ts';
import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { privateLoginForm } from './login-form.ts';
import { noiseSpeechFixture, voiceDistribution, voiceRegions } from './voice-noise-fixture.ts';
import { installVoiceNoiseProbe, installVoiceNoiseScreen } from './voice-noise-probe.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
interface Sample {
  mic: number[];
  voice: number[];
  screen: number[];
  clips: number;
  voiceStep: number;
  flags: {
    noiseSuppression?: boolean;
    autoGainControl?: boolean;
    echoCancellation?: boolean;
    channelCount?: number;
    neuralNoise?: boolean;
    modelRate?: number;
    processedChannels?: number;
  };
  voiceElements: number;
  silentElements: boolean;
  tracks: { kind: string; enabled: boolean; muted: boolean; peak: number }[];
}
interface Probe {
  begin(): number;
  read(since: number): Sample;
  close(): Promise<void>;
}
async function flags(page: Page, ns: boolean, agc: boolean) {
  await page.getByRole('button', { name: 'Настройки', exact: true }).click();
  const suppression = page.getByRole('checkbox', { name: 'Шумоподавление', exact: true });
  if ((await suppression.isChecked()) !== ns) await suppression.click();
  await expect(suppression).toBeChecked({ checked: ns });
  const automatic = page.getByRole('checkbox', { name: 'Автоматическая громкость', exact: true });
  if ((await automatic.isChecked()) !== agc) await automatic.click();
  await expect(automatic).toBeChecked({ checked: agc });
  await expect(page.getByRole('checkbox', { name: 'Автоматическая громкость', exact: true })).toBeEnabled();
  await page.keyboard.press('Escape');
}
async function measure(pages: readonly Page[]) {
  const starts = await Promise.all(
    pages.map((page) =>
      page.evaluate(() =>
        (window as unknown as { __gulVoiceNoiseProbe: Probe }).__gulVoiceNoiseProbe.begin(),
      ),
    ),
  );
  await new Promise((done) => setTimeout(done, 15_000));
  return Promise.all(
    pages.map((page, index) =>
      page.evaluate(
        (since) => (window as unknown as { __gulVoiceNoiseProbe: Probe }).__gulVoiceNoiseProbe.read(since),
        starts[index],
      ),
    ),
  );
}
function facts(samples: readonly Sample[], reference: ReturnType<typeof noiseSpeechFixture>['reference']) {
  return samples.map((sample) => ({
    mic: voiceDistribution(sample.mic),
    received: voiceDistribution(sample.voice),
    regions: voiceRegions(sample.voice, sample.voiceStep, reference),
    screen: sample.screen.length ? voiceDistribution(sample.screen) : undefined,
    clips: sample.clips,
    flags: sample.flags,
    voiceElements: sample.voiceElements,
    silentElements: sample.silentElements,
    tracks: sample.tracks,
  }));
}

test('two desktop peers preserve speech, measure fan/key suppression and maintain voice routing', async () => {
  test.skip(!fixture, 'Set GUL_ELECTRON_STAND_DIR to the isolated REALITY fixture.');
  const directory = await mkdtemp(join(tmpdir(), 'gul-voice-noise-'));
  const input = noiseSpeechFixture(
    await readFile(new URL('./testdata/noise/clean-speech.wav', import.meta.url)),
  );
  const microphone = join(directory, 'speech-noise.wav');
  await writeFile(microphone, input.wav, { mode: 0o600 });
  const address = (await readFile(resolve(fixture!, 'address'), 'utf8')).trim();
  const password = (await readFile(resolve(fixture!, 'join-password'), 'utf8')).trim();
  const apps: ElectronApplication[] = [];
  const pages: Page[] = [];
  try {
    for (let index = 0; index < 2; index++) {
      const app = await electron.launch({
        executablePath: require('electron'),
        args: [
          '.',
          '--gul-electron-test',
          '--use-fake-device-for-media-stream',
          `--use-file-for-fake-audio-capture=${microphone}`,
          // The fake WAV requires file access only in this isolated test audio service.
          // Renderer sandbox and Gul's capture guards stay enabled.
          '--disable-features=AudioServiceSandbox',
          '--mute-audio',
          `--user-data-dir=${join(directory, String(index))}`,
        ],
        env: { ...process.env, NODE_ENV: 'test', GUL_ELECTRON_TEST_CA: resolve(fixture!, 'ca.pem') },
      });
      apps.push(app);
      console.info(
        'GUL_VOICE_NOISE_SWITCHES',
        await app.evaluate(
          ({ app }, expected) => ({
            fake: app.commandLine.hasSwitch('use-fake-device-for-media-stream'),
            file: app.commandLine.getSwitchValue('use-file-for-fake-audio-capture') === expected,
          }),
          microphone,
        ),
      );
      const page = await app.firstWindow();
      pages.push(page);
      await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
      await page.evaluate(installVoiceNoiseProbe);
      await page.evaluate(installVoiceNoiseScreen);
      await privateLoginForm(page, address, password);
      await page.getByLabel('Твой ник', { exact: true }).fill(`noise-peer-${index}`);
      await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
      await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
    }
    await Promise.all(pages.map((page) => expect(page.locator('audio[data-source="voice"]')).toHaveCount(1)));
    await Promise.all(pages.map((page) => flags(page, false, false)));
    const untreated = facts(await measure(pages), input.reference);
    console.info('GUL_VOICE_NOISE_UNTREATED', JSON.stringify(untreated));
    expect(untreated[0].received.speechDb).toBeGreaterThan(-45);
    expect(untreated[1].received.speechDb).toBeGreaterThan(-45);
    await Promise.all(pages.map((page) => flags(page, true, false)));
    const suppressed = facts(await measure(pages), input.reference);
    console.info('GUL_VOICE_NOISE_NS', JSON.stringify(suppressed));
    await Promise.all(pages.map((page) => flags(page, true, true)));
    const automatic = facts(await measure(pages), input.reference);
    console.info('GUL_VOICE_NOISE_DEFAULT', JSON.stringify(automatic));
    let sharing = automatic;
    // Linux native screen audio requires real private input enumeration, which fake
    // microphone flags hide. Its capture/exclusion proof runs in the native suite.
    if (process.platform !== 'linux') {
      await Promise.all(pages.map((page) => startScreen(page)));
      await pages[0].getByRole('button', { name: /noise-peer-1.*Смотреть экран/ }).click();
      await pages[1].getByRole('button', { name: /noise-peer-0.*Смотреть экран/ }).click();
      await Promise.all(
        pages.map((page) => expect(page.locator('audio[data-source="screen"]')).toHaveCount(1)),
      );
      sharing = facts(await measure(pages), input.reference);
      console.info('GUL_VOICE_NOISE_SCREEN', JSON.stringify(sharing));
      await Promise.all(
        pages.map((page) =>
          page.getByRole('button', { name: 'Остановить демонстрацию', exact: true }).click(),
        ),
      );
    }
    const restored = facts(await measure(pages), input.reference);
    console.info('GUL_VOICE_NOISE_RESTORED', JSON.stringify(restored));
    for (let index = 0; index < 2; index++) {
      expect(untreated[index].flags.noiseSuppression).toBe(false);
      expect(suppressed[index].flags.noiseSuppression).toBe(false);
      expect(suppressed[index].flags.autoGainControl).toBe(false);
      expect(untreated[index].flags.neuralNoise).toBe(false);
      expect(suppressed[index].flags.neuralNoise).toBe(true);
      expect(suppressed[index].flags.modelRate).toBe(48000);
      expect(automatic[index].flags.autoGainControl).toBe(true);
      expect(suppressed[index].received.relativeNoiseDb).toBeLessThan(
        untreated[index].received.relativeNoiseDb - 6,
      );
      expect(suppressed[index].received.speechDb).toBeGreaterThan(untreated[index].received.speechDb - 8);
      expect(suppressed[index].regions.correlation).toBeGreaterThan(0.75);
      expect(suppressed[index].regions.keysDb).toBeLessThan(untreated[index].regions.keysDb - 15);
      for (const sample of [automatic[index], sharing[index], restored[index]]) {
        expect(sample.flags.echoCancellation).toBe(true);
        expect(sample.flags.channelCount).toBe(1);
        expect(sample.flags.processedChannels).toBe(1);
        expect(sample.flags.neuralNoise).toBe(true);
        expect(sample.flags.modelRate).toBe(48000);
        expect(sample.voiceElements).toBe(1);
        expect(sample.silentElements).toBe(true);
        expect(sample.clips).toBe(0);
        expect(sample.received.speechDb).toBeGreaterThan(-40);
        expect(sample.regions.fanDb).toBeLessThan(-65);
        expect(sample.regions.keysDb).toBeLessThan(-50);
        expect(Math.abs(sample.received.speechDb - automatic[index].received.speechDb)).toBeLessThan(3);
      }
      if (process.platform !== 'linux') expect(sharing[index].screen!.speechDb).toBeGreaterThan(-40);
    }
  } finally {
    await Promise.all(apps.map((app) => app.close()));
    await rm(directory, { recursive: true, force: true });
  }
});
