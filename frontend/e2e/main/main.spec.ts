import { expect, test, type Page, type Route } from '@playwright/test';

async function grant(route: Route, identity: string) {
  const { epoch, channelId } = route.request().postDataJSON();
  const response = await route.request().frame().page().request.post('http://127.0.0.1:8787/api/livekit/token', {
    data: { identity: `screen-${identity}`, room: 'gul-local' },
  });
  expect(response.status()).toBe(200);
  await route.fulfill({ json: { ...await response.json(), epoch, channelId, ownerIdentity: identity } });
}

async function ready(page: Page) {
  await expect(page.getByTestId('screen-share-toggle')).toHaveAttribute('data-state', 'connected');
  await expect(page.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
}

test('production MainScreen leaves lazy fallback and keeps one controller through native tree/status churn', async ({ browser }) => {
  const publisher = await browser.newPage();
  const viewer = await browser.newPage();
  let providerChunk: Route | undefined;
  let grants = 0;
  try {
    await publisher.route('**/assets/ScreenShareProvider-*.js', (route) => { providerChunk = route; });
    await publisher.route('**/test/screen-grant', (route) => { grants++; return grant(route, 'full-main-publisher'); });
    await viewer.route('**/test/screen-grant', (route) => grant(route, 'full-main-viewer'));
    await publisher.goto('/e2e/main/index.html');
    await expect.poll(() => !!providerChunk).toBe(true);
    await expect(publisher.getByTestId('screen-share-toggle')).toBeDisabled();
    await publisher.evaluate(() => window.gulMainScreenTest.churn(10));
    expect(grants).toBe(0);
    await providerChunk!.continue();
    await ready(publisher);
    await publisher.evaluate(() => window.gulMainScreenTest.churn(30));
    await ready(publisher);
    expect(grants).toBe(1);
    await expect(publisher.getByTestId('screen-share-panel')).toHaveCount(0);
    await viewer.goto('/e2e/main/index.html');
    await ready(viewer);
    await publisher.getByRole('button', { name: 'Показать экран', exact: true }).click();
    const video = viewer.locator('video[data-local="false"]');
    await expect(video).toHaveCount(1);
    await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.videoWidth)).toBe(640);
    await publisher.evaluate(() => window.gulMainScreenTest.churn(30));
    await expect(publisher.getByRole('button', { name: 'Остановить показ', exact: true })).toHaveAttribute('aria-pressed', 'true');
    expect(grants).toBe(1);
    await expect(video).toHaveCount(1);
    await publisher.getByRole('button', { name: 'Остановить показ', exact: true }).click();
    await expect(video).toHaveCount(0);
    await expect.poll(() => publisher.evaluate(() => window.gulMainScreenTest.captures())).toEqual([['ended']]);
  } finally {
    await publisher.close();
    await viewer.close();
  }
});

test('production MainScreen surfaces a grant failure and retry reaches the shared toolbar', async ({ page }) => {
  let requests = 0;
  await page.route('**/test/screen-grant', (route) => {
    requests++;
    return requests <= 3 ? route.fulfill({ status: 503 }) : grant(route, 'full-main-retry');
  });
  await page.goto('/e2e/main/index.html');
  await expect(page.getByRole('alert')).toContainText('SCREEN_GRANT_REQUEST');
  expect(requests).toBe(3);
  await expect(page.getByTestId('screen-share-toggle')).toHaveAttribute('data-state', 'disconnected');
  await page.getByRole('button', { name: 'Повторить', exact: true }).click();
  await ready(page);
  await page.evaluate(() => window.gulMainScreenTest.churn(30));
  await ready(page);
  expect(requests).toBe(4);
  await expect(page.getByTestId('screen-share-panel')).toHaveCount(0);
});

test('production MainScreen uses an explicit browser fallback when embedded WebRTC is absent', async ({ page }) => {
  let requests = 0;
  let opened = 0;
  await page.addInitScript(() => {
    // webrtc-adapter can restore the standard constructor from Chrome's alias.
    for (const name of ['RTCPeerConnection', 'webkitRTCPeerConnection', 'mozRTCPeerConnection']) {
      Object.defineProperty(window, name, { value: undefined, configurable: true });
    }
  });
  await page.route('**/test/screen-grant', (route) => { requests++; return route.fulfill({ status: 503 }); });
  await page.route('**/test/open-screen-browser', (route) => {
    opened++;
    expect(route.request().postDataJSON()).toEqual({ epoch: 7, channelId: 0 });
    return route.fulfill({ status: opened === 1 ? 503 : 204 });
  });
  await page.goto('/e2e/main/index.html');
  await expect(page.getByTestId('screen-share-toggle')).toBeEnabled();
  await expect(page.getByRole('alert')).toHaveCount(0);
  expect(opened).toBe(0);
  await page.getByTestId('screen-share-toggle').click();
  await expect.poll(() => opened).toBe(1);
  await expect(page.getByRole('alert')).toContainText('Не удалось открыть браузер');
  await page.getByTestId('screen-share-toggle').click();
  await expect.poll(() => opened).toBe(2);
  await expect(page.getByRole('alert')).toHaveCount(0);
  expect(requests).toBe(0);
  expect(await page.evaluate(() => window.gulMainScreenTest.captures())).toEqual([]);
});

test('production MainScreen uses the same browser fallback when display capture alone is missing', async ({ page }) => {
  let grants = 0;
  await page.addInitScript(() => { navigator.mediaDevices.getDisplayMedia = undefined as never; });
  await page.route('**/test/screen-grant', (route) => { grants++; return route.fulfill({ status: 503 }); });
  await page.goto('/e2e/main/index.html');
  await expect(page.getByTestId('screen-share-toggle')).toHaveAttribute('data-state', 'browser');
  await expect(page.getByTestId('screen-share-toggle')).toBeEnabled();
  expect(grants).toBe(0);
});

test('production MainScreen recovers a transient grant failure with one fresh automatic attempt', async ({ page }) => {
  let requests = 0;
  await page.route('**/test/screen-grant', (route) => {
    requests++;
    return requests === 1 ? route.fulfill({ status: 503 }) : grant(route, 'full-main-auto-retry');
  });
  await page.goto('/e2e/main/index.html');
  await ready(page);
  expect(requests).toBe(2);
  await expect(page.getByTestId('screen-share-panel')).toHaveCount(0);
  expect(await page.evaluate(() => window.gulMainScreenTest.captures())).toEqual([]);
});
