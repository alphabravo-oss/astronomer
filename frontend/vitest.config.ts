import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config.ts";

// vite.config.ts is a function (it reads `command` for __UI_GALLERY__).
export default defineConfig(async (env) =>
  mergeConfig(await viteConfig(env), {
    test: {
      // Bound jsdom/Vite worker memory when frontend and Go/Local CI gates
      // share a host. Keep per-file isolation and existing failure deadlines;
      // CPU-count-based fan-out can starve lazy-module imports and worker startup.
      maxWorkers: 4,
      environment: "jsdom",
      globals: true,
      setupFiles: ["./vitest.setup.ts"],
      include: ["src/**/*.test.{ts,tsx}"],
      exclude: ["tests/e2e/**", "node_modules/**"],
    },
  }),
);
