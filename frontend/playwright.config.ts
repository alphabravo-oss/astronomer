import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.PLAYWRIGHT_PORT || 3100);
const chromiumExecutable = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
const managedLiveServer = process.env.PLAYWRIGHT_REUSE_EXISTING_SERVER === "1";
const outputDir = process.env.PLAYWRIGHT_OUTPUT_DIR;
const htmlOutputDir = process.env.PLAYWRIGHT_HTML_OUTPUT_DIR;

export default defineConfig({
  testDir: "./tests/e2e",
  outputDir,
  reporter: htmlOutputDir
    ? [["html", { outputFolder: htmlOutputDir, open: "never" }]]
    : undefined,
  timeout: 30_000,
  expect: {
    timeout: 10_000,
  },
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    // A retry-free failure must still be diagnosable. Traces are retained only
    // for failures, while screenshots and video keep the same bounded policy.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  webServer: {
    // Preview (not dev) deliberately: it serves the built dist/ with
    // SPA-fallback semantics, so every deep-link page.goto implicitly
    // tests fallback + the real bundle.
    command: `npm run build && npx vite preview --host 127.0.0.1 --port ${port}`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: managedLiveServer || !process.env.CI,
    timeout: 180_000,
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      name: "mobile-chromium",
      use: {
        ...devices["Pixel 7"],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      name: "tablet-chromium",
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1024, height: 768 },
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      // P7.1 route-smoke crawl: one cheap render check per manifest URL, so
      // the whole tier is fully parallel and chromium-only. `test:e2e` is
      // pinned to the two tier-1 projects and never picks this up.
      name: "route-smoke",
      testDir: "./tests/e2e-smoke",
      fullyParallel: true,
      use: {
        ...devices["Desktop Chrome"],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      name: "route-smoke-mobile",
      testDir: "./tests/e2e-smoke",
      fullyParallel: true,
      use: {
        ...devices["Pixel 7"],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
    {
      // P7.2 live tier: explicit journeys against a REAL Go backend, reached
      // through the preview server's `/api` proxy (BACKEND_URL, see
      // vite.config.ts preview.proxy). Critical release journeys are
      // retry-free: an intermittent failure remains a failure and retains
      // its diagnostics.
      name: "live",
      testDir: "./tests/e2e-live",
      retries: 0,
      // Headroom for the login helper waiting out the backend's fixed-window
      // login rate limiter (up to ~60s) on top of real-network latencies.
      timeout: 120_000,
      // The admin journeys authenticate as the same bootstrap identity, and backend
      // logout bumps the per-user token cutoff (InvalidateAllTokens in
      // internal/handler/auth.go) — a parallel worker's session dies the
      // moment the login spec signs out. Serial is correct, not a workaround.
      workers: 1,
      use: {
        ...devices["Desktop Chrome"],
        launchOptions: chromiumExecutable
          ? { executablePath: chromiumExecutable, chromiumSandbox: false }
          : undefined,
      },
    },
  ],
});
