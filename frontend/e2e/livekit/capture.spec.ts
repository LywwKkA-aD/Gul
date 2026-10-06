import { expect, test } from '@playwright/test';

// Optional manual capture check: in Chrome, join as chrome-capture, share
// /livekit-source.html with tab audio, and enable its tone before running.
test('receives real screen-capture video and audio', async ({ page }) => {
  test.skip(process.env.GUL_LIVEKIT_CAPTURE_TEST !== '1', 'Requires a real OS/browser capture selection');
  await page.goto('/#livekit');
  await page.getByLabel('Имя участника').fill('capture-observer');
  await page.getByRole('button', { name: 'Войти в тестовую комнату', exact: true }).click();
  await expect(page.getByTestId('lab-status')).toHaveText('Подключено');
  const video = page.locator('video[data-participant="chrome-capture"][data-local="false"]');
  const audio = page.locator('audio[data-participant="chrome-capture"]');
  await expect(video).toHaveCount(1);
  await expect(audio).toHaveCount(1);
  await page.getByRole('button', { name: 'Включить звук', exact: true }).click();
  await video.scrollIntoViewIfNeeded();
  await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.videoWidth)).toBeGreaterThan(200);
  const initial = await video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames);
  await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames)).toBeGreaterThan(initial + 15);
  const peak = await audio.evaluate(async (el: HTMLAudioElement) => {
    const context = new AudioContext();
    await context.resume();
    const analyser = context.createAnalyser();
    context.createMediaStreamSource(el.srcObject as MediaStream).connect(analyser);
    const samples = new Float32Array(analyser.fftSize);
    let peak = 0;
    for (let i = 0; i < 30; i++) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      analyser.getFloatTimeDomainData(samples);
      peak = Math.max(peak, ...samples.map(Math.abs));
    }
    await context.close();
    return peak;
  });
  expect(peak).toBeGreaterThan(0.01);
  console.log(`Real captured audio peak: ${peak.toFixed(4)}; moving video decoded`);
  await page.getByRole('button', { name: 'Выйти', exact: true }).click();
});
