import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e/panel',
  testMatch: '*.spec.ts',
  timeout: 45_000,
  expect: { timeout: 15_000 },
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:9251',
    headless: true,
    launchOptions: {
      executablePath: process.env.GUL_TEST_BROWSER || (process.platform === 'darwin'
        ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : undefined),
    },
    trace: 'off', screenshot: 'off', video: 'off',
  },
  webServer: {
    command: 'npx vite --config vite.panel.config.ts',
    url: 'http://127.0.0.1:9251/e2e/panel/index.html',
    timeout: 20_000,
  },
});
