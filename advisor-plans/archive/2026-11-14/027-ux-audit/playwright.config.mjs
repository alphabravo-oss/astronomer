import { createRequire } from 'node:module';
const require = createRequire(new URL('../../frontend/package.json', import.meta.url));
const { defineConfig } = require('@playwright/test');
const run = process.env.UX_AUDIT_RUN || 'initial';
export default defineConfig({
  testDir: '.', testMatch: 'review.spec.ts', workers: 1, retries: 0,
  timeout: 120000, expect: { timeout: 30000 },
  outputDir: run === 'initial' ? './artifacts' : `./artifacts-${run}`,
  reporter: [['list'], ['json', { outputFile: `./advisor-plans/027-ux-audit/results-${run}.json` }]],
  use: { baseURL: 'http://127.0.0.1:32127', browserName: 'chromium',
    screenshot: 'only-on-failure', trace: 'retain-on-failure', locale: 'en-US', timezoneId: 'UTC' },
});
