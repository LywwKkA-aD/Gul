import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: './e2e',
  timeout: 240_000,
  workers: 1,
  reporter: 'line',
  use: { trace: 'off', screenshot: 'off', video: 'off' },
  outputDir: 'test-results',
});
