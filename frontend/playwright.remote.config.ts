import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e/remote', testMatch: '*.spec.ts', timeout: 90_000,
  expect: { timeout: 30_000 }, workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:9253', headless: true,
    launchOptions: {
      executablePath: process.env.GUL_TEST_BROWSER || (process.platform === 'darwin'
        ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : undefined),
      args: ['--mute-audio'],
    },
    trace: 'off', screenshot: 'off', video: 'off',
  },
  webServer: process.env.GUL_REMOTE_E2E === '1' ? {
    command: 'npx vite build --config vite.remote.config.ts && npx vite preview --config vite.remote.config.ts',
    url: 'http://127.0.0.1:9253/e2e/remote/index.html', timeout: 30_000,
  } : undefined,
});
