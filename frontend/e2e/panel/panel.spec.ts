import { expect, test, type Page, type Route } from '@playwright/test';

async function grantRoute(route: Route, identity: string) {
  const { epoch, channelId } = route.request().postDataJSON();
  const response = await route.request().frame().page().request.post('http://127.0.0.1:8787/api/livekit/token', {
    data: { identity: `screen-${identity}`, room: 'gul-local' },
  });
  expect(response.status()).toBe(200);
  await route.fulfill({ json: { ...await response.json(), epoch, channelId, ownerIdentity: identity } });
}

async function openPanel(page: Page, identity: string) {
  await page.route('**/test/screen-grant', (route) => grantRoute(route, identity));
  await observePeers(page);
  await page.goto('/e2e/panel/index.html');
  await expect(page.getByTestId('screen-share-toggle')).toHaveAttribute('data-state', 'connected');
}

async function observePeers(page: Page) {
  await page.addInitScript(() => {
    const Peer = window.RTCPeerConnection;
    window.gulPeerCount = 0;
    window.gulPeerIceServers = [];
    window.RTCPeerConnection = class extends Peer {
      constructor(config?: RTCConfiguration) {
        super(config);
        window.gulPeerCount++;
        window.gulPeerIceServers.push(config?.iceServers ?? []);
      }
    };
  });
}

test('panel renders real remote video without browser audio and releases capture on channel change', async ({ browser }) => {
  const publisher = await browser.newPage();
  const viewer = await browser.newPage();
  try {
    await openPanel(publisher, 'panel-publisher');
    await openPanel(viewer, 'panel-viewer');
    await expect(publisher.getByTestId('screen-share-panel')).toHaveCount(0);
    await expect(viewer.getByTestId('screen-share-panel')).toHaveCount(0);
    const toggle = publisher.getByTestId('screen-share-toggle');
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await expect(publisher.locator('footer').getByRole('button')).toHaveCount(4);
    expect(await toggle.evaluate((element) => element.getBoundingClientRect().width)).toBe(28);
    expect(await publisher.evaluate(() => window.gulPanelTest.captureStates())).toEqual([]);
    await publisher.getByRole('button', { name: 'Показать экран', exact: true }).click();
    const video = viewer.locator('video[data-local="false"]');
    await expect(video).toHaveCount(1);
    await video.scrollIntoViewIfNeeded();
    await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.videoWidth)).toBe(1280);
    const first = await video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames);
    await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.getVideoPlaybackQuality().totalVideoFrames)).toBeGreaterThan(first + 5);
    await expect(publisher.getByTestId('screen-share-status')).toContainText('со звуком');
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    await expect(viewer.locator('audio')).toHaveCount(0);
    await expect(publisher.locator('audio')).toHaveCount(0);
    for (const page of [publisher, viewer]) {
      expect(await page.evaluate(() => window.gulPeerCount)).toBeGreaterThan(0);
      expect(await page.evaluate(() => window.gulPeerIceServers.every((servers) => servers.length === 0))).toBe(true);
    }
    await viewer.evaluate(() => window.gulPanelTest.deafen());
    await expect(viewer.getByTestId('screen-share-status')).toContainText('Звук демонстраций выключен');
    await publisher.getByRole('button', { name: 'Остановить показ', exact: true }).click();
    await expect(video).toHaveCount(0);
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await expect(publisher.getByTestId('screen-share-panel')).toHaveCount(0);
    await publisher.getByRole('button', { name: 'Показать экран', exact: true }).click();
    await expect(video).toHaveCount(1);
    await publisher.evaluate(() => window.gulPanelTest.status({ epoch: 2, selfChannel: 2 }));
    await expect.poll(() => publisher.evaluate(() => window.gulPanelTest.captureStates())).toEqual([['ended', 'ended'], ['ended', 'ended']]);
    await expect(video).toHaveCount(0);
    await expect(publisher.getByTestId('screen-share-toggle')).toHaveAttribute('data-state', 'connected');
    await expect(publisher.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
    await expect(publisher.locator('video')).toHaveCount(0);
  } finally {
    await publisher.close();
    await viewer.close();
  }
});

test('late grant after disconnect cannot reconnect an unmounted screen panel', async ({ page }) => {
  let pending: Route | undefined;
  await page.route('**/test/screen-grant', (route) => { pending = route; });
  await observePeers(page);
  await page.goto('/e2e/panel/index.html');
  await expect.poll(() => !!pending).toBe(true);
  await page.evaluate(() => window.gulPanelTest.status({ state: 'disconnected' }));
  await expect(page.getByTestId('no-screen-session')).toBeVisible();
  await grantRoute(pending!, 'late-panel');
  await expect.poll(() => page.evaluate(() => window.gulPanelGrantReplies)).toBe(1);
  expect(await page.evaluate(() => window.gulPeerCount)).toBe(0);
  await expect(page.getByTestId('screen-share-panel')).toHaveCount(0);
});

test('late display selection is stopped after reconnect without reviving capture', async ({ page }) => {
  await openPanel(page, 'late-capture');
  await page.evaluate(() => window.gulPanelTest.deferCapture());
  await page.getByRole('button', { name: 'Показать экран', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Отменить выбор экрана', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('screen-share-panel')).toHaveCount(0);
  await page.evaluate(() => window.gulPanelTest.status({ state: 'reconnecting' }));
  await expect(page.getByTestId('no-screen-session')).toBeVisible();
  await page.evaluate(() => window.gulPanelTest.releaseCapture());
  await expect.poll(() => page.evaluate(() => window.gulPanelTest.captureStates())).toEqual([['ended', 'ended']]);
  await page.evaluate(() => window.gulPanelTest.status({ epoch: 2 }));
  await expect(page.getByTestId('screen-share-toggle')).toHaveAttribute('data-state', 'connected');
  await expect(page.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
  await expect(page.locator('video, audio')).toHaveCount(0);
});
