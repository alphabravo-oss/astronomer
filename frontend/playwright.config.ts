import { defineConfig, devices } from '@playwright/test';

const port = Number(process.env.PLAYWRIGHT_PORT || 3100);
const chromiumExecutable = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;

export default defineConfig({
  testDir: './tests/e2e',
  timeout: 30_000,
  expect: {
    timeout: 10_000,
  },
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: 'on-first-retry',
  },
  webServer: {
    // Preview (not dev) deliberately: it serves the built dist/ with
    // SPA-fallback semantics, so every deep-link page.goto implicitly
    // tests fallback + the real bundle.
    command: `npm run build && npx vite preview --host 127.0.0.1 --port ${port}`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      name: 'mobile-chromium',
      use: {
        ...devices['Pixel 7'],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      // P7.1 route-smoke crawl: one cheap render check per manifest URL, so
      // the whole tier is fully parallel and chromium-only. `test:e2e` is
      // pinned to the two tier-1 projects and never picks this up.
      name: 'route-smoke',
      testDir: './tests/e2e-smoke',
      fullyParallel: true,
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      // P7.2 live tier: exactly 4 specs against a REAL Go backend, reached
      // through the preview server's `/api` proxy (BACKEND_URL, see
      // vite.config.ts preview.proxy). retries: 1 for this project only —
      // the sanctioned flake budget for real-network specs (R5); a spec
      // flaking >2×/week post-merge moves to nightly, never deleted.
      name: 'live',
      testDir: './tests/e2e-live',
      retries: 1,
      // Headroom for the login helper waiting out the backend's fixed-window
      // login rate limiter (up to ~60s) on top of real-network latencies.
      timeout: 120_000,
      // All 4 specs authenticate as the same bootstrap admin, and backend
      // logout bumps the per-user token cutoff (InvalidateAllTokens in
      // internal/handler/auth.go) — a parallel worker's session dies the
      // moment the login spec signs out. Serial is correct, not a workaround.
      workers: 1,
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
  ],
});
