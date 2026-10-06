import { defineConfig } from "vite";
import packageMetadata from "./package.json" with { type: "json" };
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";

export default defineConfig(({ command }) => ({
  plugins: [
    tailwindcss(),
    tanstackRouter({
      target: "react",
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
      routeFileIgnorePattern: "\\.test\\.(ts|tsx)$",
      autoCodeSplitting: true,
    }), // MUST precede react()
    react(),
  ],
  resolve: { tsconfigPaths: true },
  define: {
    __APP_VERSION__: JSON.stringify(
      process.env.VERSION ?? packageMetadata.version,
    ),
    __BUILD_COMMIT__: JSON.stringify(process.env.GIT_COMMIT ?? "unknown"),
    __BUILD_DATE__: JSON.stringify(process.env.BUILD_DATE ?? "unknown"),
    __BUILD_NODE_VERSION__: JSON.stringify(process.version),
    // Dev-only /dashboard/dev/ui gallery (plan 031 phase 8): on for the dev
    // server and for builds with VITE_UI_GALLERY=1 (the Playwright web server).
    // A define folds to a literal so production builds drop the dynamic import.
    __UI_GALLERY__: JSON.stringify(
      command === "serve" || process.env.VITE_UI_GALLERY === "1",
    ),
  },
  server: {
    host: process.env.VITE_DEV_HOST ?? "127.0.0.1",
    port: Number(process.env.PORT) || 3000,
    proxy: {
      "/api": {
        target: process.env.BACKEND_URL ?? "http://localhost:8001",
        ws: true,
      },
    },
  },
  preview: {
    proxy: {
      "/api": {
        target: process.env.BACKEND_URL ?? "http://localhost:8001",
        ws: true,
      },
    },
  },
  build: {
    manifest: true,
    outDir: "dist",
    // CSP keeps font-src limited to same-origin files. Do not turn small font
    // subsets into data: URLs that browsers must reject.
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 650,
  },
}));
