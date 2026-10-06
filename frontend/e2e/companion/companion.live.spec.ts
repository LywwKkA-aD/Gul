import { existsSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { expect, test } from '@playwright/test';
import { captureStates, realityRoutes, syntheticCapture } from './capture';

function privateLaunch(path: string | undefined): string {
  try {
    if (!path || !isAbsolute(path) || (statSync(path).mode & 0o077)) throw new Error();
    const url = new URL(readFileSync(path, 'utf8').trim());
    if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.pathname !== '/screen.html' ||
        url.username || url.password || url.search || !/^#[0-9a-f]{64}$/.test(url.hash)) throw new Error();
    return url.href;
  } catch { throw new Error('A private localhost companion launch file is required'); }
}

test('actual Go bridge grants the built companion and a native channel change stops capture', async ({ page, browser }) => {
  const launch = privateLaunch(process.env.GUL_BROWSER_COMPANION_URL_FILE);
  const control = process.env.GUL_BROWSER_COMPANION_CONTROL_FILE;
  if (!control || !isAbsolute(control)) throw new Error('A private companion control-file path is required');
  await syntheticCapture(page);
  const receiverFile = process.env.GUL_BROWSER_COMPANION_RECEIVER_URL_FILE;
  const receiver = receiverFile ? await browser.newPage() : undefined;
  if (receiver) await syntheticCapture(receiver);
  let passed = false;
  try {
    // Do not let a browser navigation error print its fragment capability.
    try { await page.goto(launch); } catch { throw new Error('Companion navigation failed'); }
    await expect(page.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
    expect(new URL(page.url()).hash === '').toBe(true);
    if (receiver) {
      try { await receiver.goto(privateLaunch(receiverFile)); } catch { throw new Error('Receiver navigation failed'); }
      await expect(receiver.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
    }
    expect(await captureStates(page)).toEqual([]);
    await page.getByRole('button', { name: 'Показать экран', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Остановить показ', exact: true })).toBeEnabled();
    if (receiver) {
      const video = receiver.locator('video[data-local="false"]');
      await expect(video).toHaveCount(1);
      await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.videoWidth)).toBe(640);
      const frames = await video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames);
      await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames)).toBeGreaterThan(frames + 5);
      await expect(receiver.locator('audio')).toHaveCount(0);
    }
    const audioReady = process.env.GUL_BROWSER_COMPANION_AUDIO_READY_FILE;
    if (!audioReady || !isAbsolute(audioReady)) throw new Error('A private native-audio confirmation file is required');
    await expect.poll(() => existsSync(audioReady)).toBe(true);
    if (process.env.GUL_BROWSER_COMPANION_REALITY === '1') for (const client of [page, ...(receiver ? [receiver] : [])]) {
      const routes = await realityRoutes(client);
      expect(routes.length).toBeGreaterThan(0);
      expect(routes.every((route) => route.relayOnly && route.onlyLocalTurn)).toBe(true);
      const selected = routes.flatMap((route) => route.selected);
      expect(selected.length).toBeGreaterThan(0);
      expect(selected.every(Boolean)).toBe(true);
    }
    writeFileSync(control, 'channel\n', { mode: 0o600 });
    await expect(page.getByText('Сессия завершена. Откройте демонстрации снова из Gul.', { exact: true })).toBeVisible();
    await expect.poll(() => captureStates(page)).toEqual(['ended', 'ended']);
    await expect(page.locator('video')).toHaveCount(0);
    passed = true;
  } finally {
    await receiver?.close();
    await page.close();
    writeFileSync(control, passed ? 'done\n' : 'failed\n', { mode: 0o600 });
  }
});
