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
  ],
});
