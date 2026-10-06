import { expect, test } from '@playwright/test';

test('two clients receive moving video and audible audio, then clean up and reconnect', async ({ browser }) => {
  const publisher = await browser.newPage();
  const viewer = await browser.newPage();
  for (const [page, name] of [[publisher, 'publisher'], [viewer, 'viewer']] as const) {
    await page.goto('/#livekit');
    await page.getByLabel('Имя участника').fill(name);
    await page.getByRole('button', { name: 'Войти в тестовую комнату', exact: true }).click();
    await expect(page.getByTestId('lab-status')).toHaveText('Подключено');
  }
  await publisher.getByRole('button', { name: 'Тест: картинка и тон', exact: true }).click();
  const video = viewer.locator('video[data-local="false"][data-participant="publisher"]');
  const audio = viewer.locator('audio[data-participant="publisher"]');
  await expect(video).toHaveCount(1);
  await expect(audio).toHaveCount(1);
  await viewer.getByRole('button', { name: 'Включить звук', exact: true }).click();
  // Adaptive subscription pauses screens outside the visible viewport.
  await video.scrollIntoViewIfNeeded();
  await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.videoWidth)).toBe(1280);
  const firstFrame = await video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames);
  await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames)).toBeGreaterThan(firstFrame + 15);
  const energy = await audio.evaluate(async (el: HTMLAudioElement) => {
    const context = new AudioContext();
    await context.resume();
    const source = context.createMediaStreamSource(el.srcObject as MediaStream);
    const analyser = context.createAnalyser();
    source.connect(analyser);
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
  expect(energy).toBeGreaterThan(0.01);
  // The sender never renders its own audio track.
  await expect(publisher.locator('audio[data-participant="publisher"]')).toHaveCount(0);
  await publisher.getByRole('button', { name: 'Остановить показ', exact: true }).click();
  await expect(video).toHaveCount(0);
  await expect(audio).toHaveCount(0);
  // A second publication and a leave exercise real SFU track removal.
  await publisher.getByRole('button', { name: 'Тест: картинка и тон', exact: true }).click();
  await expect(video).toHaveCount(1);
  await publisher.getByRole('button', { name: 'Выйти', exact: true }).click();
  await expect(video).toHaveCount(0);
  await expect(audio).toHaveCount(0);
  await publisher.getByRole('button', { name: 'Войти в тестовую комнату', exact: true }).click();
  await expect(publisher.getByTestId('lab-status')).toHaveText('Подключено');
  await publisher.getByRole('button', { name: 'Выйти', exact: true }).click();
  await viewer.getByRole('button', { name: 'Выйти', exact: true }).click();
  await publisher.close();
  await viewer.close();
});
