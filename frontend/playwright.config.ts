import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { defineConfig, devices } from '@playwright/test';

/** Port of the production preview server used by test/e2e/pwa.spec.ts. */
const previewPort = 4173;
// Outside dist/, which may belong to another user in the dev containers.
const previewDir = join(tmpdir(), 'pulse-e2e-preview');

export default defineConfig({
  testDir: './test/e2e',
  globalSetup: process.env.PLAYWRIGHT_SEED_TEST_USERS === 'true' ? './test/e2e/global-setup.ts' : undefined,
  fullyParallel: true,
  reporter: 'list',
  expect: {
    toHaveScreenshot: {
      animations: 'disabled',
      caret: 'hide',
      maxDiffPixelRatio: 0.01,
    },
  },
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://127.0.0.1:5173',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    ...devices['Desktop Chrome'],
    launchOptions: {
      executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || '/usr/bin/chromium',
      args: ['--no-sandbox'],
    },
  },
  webServer: [{
    command: 'npm run dev -- --host 127.0.0.1',
    url: 'http://127.0.0.1:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  }, {
    // A production build for the service worker tests (pwa.spec.ts); the dev
    // server registers no service worker. It proxies /api like the dev server.
    command: `npx vite build --outDir ${previewDir} --emptyOutDir --logLevel warn && npx vite preview --outDir ${previewDir} --host 127.0.0.1 --port ${previewPort} --strictPort`,
    url: `http://127.0.0.1:${previewPort}`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  }],
});
