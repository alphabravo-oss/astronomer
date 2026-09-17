import { defineConfig } from "vite";
import packageMetadata from "./package.json" with { type: "json" };
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";

export default defineConfig({
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
    chunkSizeWarningLimit: 650,
  },
});
