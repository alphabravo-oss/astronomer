import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.PLAYWRIGHT_COMPONENT_PORT || 3102);
export default defineConfig({
  testDir: "./tests/browser-components",
  testMatch: "**/*.spec.ts",
  workers: 1,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  outputDir:
    process.env.PLAYWRIGHT_OUTPUT_DIR || ".cache/browser-component-results",
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: `npx vite build --config tests/browser-components/vite.config.ts && npx vite preview --config tests/browser-components/vite.config.ts --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
    timeout: 180_000,
  },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"] } },
    {
      name: "tablet",
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1024, height: 768 },
      },
    },
    { name: "mobile", use: { ...devices["Pixel 7"] } },
  ],
});
