import { defineConfig } from '@playwright/test';

const live = !!process.env.GUL_BROWSER_COMPANION_URL_FILE;
export default defineConfig({
  testDir: './e2e/companion', testMatch: live ? '*.live.spec.ts' : 'companion.spec.ts',
  timeout: 45_000, expect: { timeout: 15_000 }, workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:9254', headless: true,
    launchOptions: { executablePath: process.env.GUL_TEST_BROWSER || (process.platform === 'darwin'
      ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : undefined), args: ['--mute-audio'] },
    trace: 'off', screenshot: 'off', video: 'off',
  },
  webServer: live ? undefined : {
    command: 'npm run build && npx vite preview --host 127.0.0.1 --port 9254 --strictPort',
    url: 'http://127.0.0.1:9254/screen.html', timeout: 30_000,
  },
});
