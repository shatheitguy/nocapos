import { defineConfig } from '@playwright/test';

// NoCap Desktop parity (e2e/parity.spec.ts): the browser build and the
// screen's build, served side by side. Build both first:
//   npm run build && npm run build:shell
export default defineConfig({
  testDir: 'e2e',
  timeout: 90_000,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: { browserName: 'chromium', headless: true, actionTimeout: 15_000 },
  webServer: [
    { command: 'npx vite preview --port 4173 --strictPort', port: 4173, reuseExistingServer: true },
    { command: 'npx vite preview --config vite.shell.config.ts --port 4174 --strictPort', port: 4174, reuseExistingServer: true },
  ],
});
