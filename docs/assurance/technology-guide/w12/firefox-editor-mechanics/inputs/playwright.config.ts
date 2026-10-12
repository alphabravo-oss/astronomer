import { defineConfig, devices } from "@playwright/test";
import { fileURLToPath } from "node:url";

const port = Number(process.env.PLAYWRIGHT_MONACO_PORT || "32257");
export default defineConfig({
  testDir: ".",
  testMatch: "editor.spec.ts",
  workers: 1,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [["json", { outputFile: process.env.MONACO_JSON_REPORT }]],
  outputDir: process.env.MONACO_OUTPUT_DIR,
  use: {
    ...devices["Desktop Firefox"],
    baseURL: `http://127.0.0.1:${port}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: `node node_modules/vite/bin/vite.js --config .cache/monaco-input-characterization/vite.config.mjs --configLoader runner --host 127.0.0.1 --port ${port} --strictPort`,
    cwd: fileURLToPath(new URL("../..", import.meta.url)),
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
    timeout: 45_000,
  },
  projects: [{ name: "scoped-firefox-editor" }],
});
