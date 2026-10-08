import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { privateLoginForm } from './login-form.ts';
import { noiseSpeechFixture, voiceDistribution, voiceRegions } from './voice-noise-fixture.ts';
import { installVoiceNoiseProbe } from './voice-noise-probe.ts';
import { installVoicePipelineProbe, type VoicePipeline } from './voice-pipeline-probe.ts';
import { speechFidelity } from './voice-pipeline-metrics.ts';

const require = createRequire(import.meta.url);
const fixture = process.env.GUL_ELECTRON_STAND_DIR;
interface Sample {
  voice: number[];
  voiceStep: number;
  clips: number;
}
interface Probe {
  begin(): number;
  read(since: number): Sample;
}
interface PipelineProbe {
  beginPCM(): Promise<number>;
  readPCM(since: number): number[];
  flags(): ReturnType<PipelineProbe['replace']> extends Promise<infer R> ? R : never;
  replace(policy: VoicePipeline): Promise<{
    browserNoise: boolean;
    neuralNoise: boolean;
    automaticGain: boolean;
    echoCancellation: boolean;
    channelCount: number;
    processedChannels: number;
    sampleRate: number;
  }>;
  close(): Promise<void>;
}
const policies: readonly (VoicePipeline & { readonly id: string })[] = [
  { id: 'combined-agc', browserNoise: true, neuralNoise: true, automaticGain: true },
  { id: 'neural-agc', browserNoise: false, neuralNoise: true, automaticGain: true },
  { id: 'combined-fixed', browserNoise: true, neuralNoise: true, automaticGain: false },
  { id: 'neural-fixed', browserNoise: false, neuralNoise: true, automaticGain: false },
  { id: 'browser-agc', browserNoise: true, neuralNoise: false, automaticGain: true },
];

for (const speechScale of [1, 0.2])
  test(`isolated voice pipeline compares NS and AGC at speech scale ${speechScale} through encoded REALITY peers`, async () => {
    test.skip(!fixture, 'Set GUL_ELECTRON_STAND_DIR to the pinned isolated audio fixture.');
    const directory = await mkdtemp(join(tmpdir(), 'gul-voice-pipeline-'));
    const speech = await readFile(new URL('./testdata/noise/clean-speech.wav', import.meta.url));
    const input = noiseSpeechFixture(speech, true, speechScale);
    const clean = noiseSpeechFixture(speech, false, speechScale);
    const cleanPCM = Float32Array.from(
      { length: clean.reference.frames },
      (_, index) => clean.wav.readInt16LE(44 + index * 2) / 32768,
    );
    const regionReference = {
      ...input.reference,
      envelope: input.reference.envelope.map((value) => value / speechScale),
    };
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
            '--disable-features=AudioServiceSandbox',
            '--mute-audio',
            `--user-data-dir=${join(directory, String(index))}`,
          ],
          env: { ...process.env, NODE_ENV: 'test', GUL_ELECTRON_TEST_CA: resolve(fixture!, 'ca.pem') },
        });
        apps.push(app);
        const page = await app.firstWindow();
        pages.push(page);
        await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
        await page.evaluate(installVoiceNoiseProbe);
        await page.evaluate(installVoicePipelineProbe);
        await privateLoginForm(page, address, password);
        await page.getByLabel('Твой ник', { exact: true }).fill(`pipeline-peer-${index}`);
        await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
        await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible({ timeout: 25_000 });
      }
      await Promise.all(
        pages.map((page) => expect(page.locator('audio[data-source="voice"]')).toHaveCount(1)),
      );
      const selected = speechScale === 1 ? policies : policies.slice(0, 2);
      for (const policy of selected) {
        await Promise.all(
          pages.map((page) =>
            page.evaluate(
              (policy) =>
                (
                  window as unknown as { __gulVoicePipelineProbe: PipelineProbe }
                ).__gulVoicePipelineProbe.replace(policy),
              policy,
            ),
          ),
        );
        // Each recapture begins with a cold APM. Let a full fixed speech loop pass
        // before comparing it so an AGC startup ramp does not decide the result.
        await new Promise((done) => setTimeout(done, Math.ceil(input.seconds * 1000)));
        const flags = await Promise.all(
          pages.map((page) =>
            page.evaluate(() =>
              (
                window as unknown as { __gulVoicePipelineProbe: PipelineProbe }
              ).__gulVoicePipelineProbe.flags(),
            ),
          ),
        );
        flags.forEach((actual) => {
          expect(actual).toMatchObject({
            browserNoise: policy.browserNoise,
            neuralNoise: policy.neuralNoise,
            automaticGain: policy.automaticGain,
            echoCancellation: true,
            channelCount: 1,
            processedChannels: 1,
            sampleRate: 48000,
          });
        });
        const pcmStarts = await Promise.all(
          pages.map((page) =>
            page.evaluate(() =>
              (
                window as unknown as { __gulVoicePipelineProbe: PipelineProbe }
              ).__gulVoicePipelineProbe.beginPCM(),
            ),
          ),
        );
        const starts = await Promise.all(
          pages.map((page) =>
            page.evaluate(() =>
              (window as unknown as { __gulVoiceNoiseProbe: Probe }).__gulVoiceNoiseProbe.begin(),
            ),
          ),
        );
        await new Promise((done) => setTimeout(done, Math.ceil(input.seconds * 1000) + 1000));
        const samples = await Promise.all(
          pages.map((page, index) =>
            page.evaluate(
              (since) =>
                (window as unknown as { __gulVoiceNoiseProbe: Probe }).__gulVoiceNoiseProbe.read(since),
              starts[index],
            ),
          ),
        );
        const decoded = await Promise.all(
          pages.map((page, index) =>
            page.evaluate(
              (since) =>
                (
                  window as unknown as { __gulVoicePipelineProbe: PipelineProbe }
                ).__gulVoicePipelineProbe.readPCM(since),
              pcmStarts[index],
            ),
          ),
        );
        const metrics = samples.map((sample, index) => ({
          flags: flags[index],
          distribution: voiceDistribution(sample.voice),
          regions: voiceRegions(sample.voice, sample.voiceStep, regionReference),
          speechFidelity: speechFidelity(cleanPCM, Float32Array.from(decoded[index])),
          clips: sample.clips,
        }));
        console.info('GUL_VOICE_PIPELINE', JSON.stringify({ id: policy.id, speechScale, metrics }));
        metrics.forEach((sample) => {
          expect(sample.clips).toBe(0);
          expect(sample.regions.correlation).toBeGreaterThan(0.7);
          expect(sample.regions.speechDb).toBeGreaterThan(-55);
        });
      }
    } finally {
      await Promise.allSettled(
        pages.map((page) =>
          page.evaluate(() =>
            (window as unknown as { __gulVoicePipelineProbe: PipelineProbe }).__gulVoicePipelineProbe.close(),
          ),
        ),
      );
      await Promise.all(apps.map((app) => app.close()));
      await rm(directory, { recursive: true, force: true });
    }
  });
