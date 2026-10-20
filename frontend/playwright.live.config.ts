import { defineConfig, devices } from "@playwright/test";

import { LIVE_ADMIN_AUTH_STATE } from "./tests/e2e-live/live.helpers";

// Live end-to-end tier: every spec drives the REAL Go server (started by
// `scripts/dev.sh up`: Air-built binary behind the Vite dev proxy). No route
// stubs, no seeded cookies — the auth setup project logs in through the real
// form and persists the HttpOnly session as storageState (guide ch.07/08).
//
//   scripts/dev.sh up && cd frontend && npx playwright test --config=playwright.live.config.ts
const port = Number(process.env.PLAYWRIGHT_LIVE_PORT || 3000);
const chromiumExecutable = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;

export default defineConfig({
  testDir: "./tests/e2e-live",
  outputDir: "./e2e-results/live",
  timeout: 120_000,
  retries: 0,
  // Every journey authenticates as the bootstrap admin and logout bumps the
  // per-user revocation cutoff, so the suite is serial by design.
  workers: 1,
  fullyParallel: false,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [["list"], ["junit", { outputFile: "e2e-results/live-junit.xml" }]] : "list",
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://127.0.0.1:${port}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    launchOptions: chromiumExecutable
      ? { executablePath: chromiumExecutable, chromiumSandbox: false }
      : undefined,
  },
  projects: [
    {
      name: "live-auth-setup",
      testMatch: /live-auth\.setup\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      // Specs that reuse the persisted real session.
      name: "live",
      dependencies: ["live-auth-setup"],
      testMatch: /(clusters|settings|metrics)\.live\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: LIVE_ADMIN_AUTH_STATE,
      },
    },
    {
      // Journeys that drive the real login/logout/refresh paths themselves.
      // Logout bumps the admin's token revocation cutoff, so these run AFTER
      // the storageState specs (dependencies order them) and start clean.
      name: "live-journeys",
      dependencies: ["live"],
      testIgnore: [
        /live-auth\.setup\.ts/,
        /(clusters|settings|metrics)\.live\.spec\.ts/,
        // Needs an adopted member cluster plus LIVE_FIXTURE_* seed data
        // (cluster, project, rollouts, Loki output, Velero backup, restricted
        // user). Selected explicitly by exporting LIVE_FIXTURE_CLUSTER_ID.
        ...(process.env.LIVE_FIXTURE_CLUSTER_ID ? [] : [/enterprise-journeys\.live\.spec\.ts/]),
      ],
      use: {
        ...devices["Desktop Chrome"],
        storageState: { cookies: [], origins: [] },
      },
    },
  ],
});
