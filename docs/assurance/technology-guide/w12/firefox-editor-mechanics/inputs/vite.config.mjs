import { defineConfig } from "../../node_modules/vite/dist/node/index.js";
import react from "../../node_modules/@vitejs/plugin-react/dist/index.js";
import { fileURLToPath } from "node:url";

export default defineConfig({
  root: fileURLToPath(new URL(".", import.meta.url)),
  cacheDir: fileURLToPath(new URL("./vite-cache", import.meta.url)),
  // Assets were copied from the immutable app build; no CDN or cache mutation.
  publicDir: fileURLToPath(new URL("../../dist", import.meta.url)),
  plugins: [react()],
  resolve: { alias: { "@": fileURLToPath(new URL("../../src", import.meta.url)) } },
  server: { host: "127.0.0.1", strictPort: true },
});
