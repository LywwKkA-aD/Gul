import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e/main',
  testMatch: '*.spec.ts',
  timeout: 45_000,
  expect: { timeout: 15_000 },
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:9252',
    headless: true,
    launchOptions: {
      executablePath: process.env.GUL_TEST_BROWSER || (process.platform === 'darwin'
        ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : undefined),
    },
    trace: 'off', screenshot: 'off', video: 'off',
  },
  webServer: {
    command: 'npx vite build --config vite.mainscreen.config.ts && npx vite preview --config vite.mainscreen.config.ts',
    url: 'http://127.0.0.1:9252/e2e/main/index.html',
    timeout: 30_000,
  },
});
