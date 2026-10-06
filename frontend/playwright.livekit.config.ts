import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e/livekit',
  timeout: 45_000,
  expect: { timeout: 20_000 },
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:8787',
    headless: true,
    launchOptions: {
      executablePath: process.env.GUL_TEST_BROWSER || (process.platform === 'darwin'
        ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
        : undefined),
    },
    // Traces may contain a temporary room token; never persist them.
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
});
