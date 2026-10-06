import { expect, test, type Page } from '@playwright/test';
import { captureStates, syntheticCapture } from './capture';

test('built browser companion keeps launch credentials private and stops real media after its native session expires', async ({ browser }) => {
  const pages: Page[] = [];
  let stale = false;
  let closed = 0;
  const code = 'a'.repeat(64);
  try {
    for (const identity of ['browser-publisher', 'browser-viewer']) {
      const page = await browser.newPage();
      pages.push(page);
      await syntheticCapture(page);
      await page.route('**/api/screen/*', async (route) => {
        const path = new URL(route.request().url()).pathname;
        const expected = path.endsWith('/open') ? code : 'test-memory-only-bearer';
        expect(route.request().headers().authorization === `Bearer ${expected}`).toBe(true);
        if (path.endsWith('/open')) return route.fulfill({ json: { token: expected === code ? 'test-memory-only-bearer' : '', epoch: 1, channelId: 1, serverOrigin: 'http://127.0.0.1:8787' } });
        if (path.endsWith('/state')) return route.fulfill(stale ? { status: 409 } : { json: { epoch: 1, channelId: 1 } });
        if (path.endsWith('/close')) { closed++; return route.fulfill({ status: 204 }); }
        const response = await page.request.post('http://127.0.0.1:8787/api/livekit/token', { data: { identity, room: 'gul-local' } });
        expect(response.ok()).toBe(true);
        return route.fulfill({ json: { ...await response.json(), epoch: 1, channelId: 1, ownerIdentity: `voice-${identity}` } });
      });
      await page.goto(`/screen.html#${code}`);
      await expect(page.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
      expect(await page.evaluate(() => location.hash === '' && !Object.values(localStorage).some((value) => String(value).includes('test-memory-only-bearer')))).toBe(true);
      expect(await captureStates(page)).toEqual([]);
    }
    const [publisher, viewer] = pages;
    await publisher.getByRole('button', { name: 'Показать экран', exact: true }).click();
    const video = viewer.locator('video[data-local="false"]');
    await expect(video).toHaveCount(1);
    await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.videoWidth)).toBe(640);
    await expect(viewer.locator('audio')).toHaveCount(0);
    stale = true;
    await expect(publisher.getByText('Сессия завершена. Откройте демонстрации снова из Gul.', { exact: true })).toBeVisible();
    await expect.poll(() => captureStates(publisher)).toEqual(['ended', 'ended']);
    await expect.poll(() => closed).toBe(2);
    await expect(publisher.locator('video')).toHaveCount(0);
  } finally { await Promise.allSettled(pages.map((page) => page.close())); }
});
