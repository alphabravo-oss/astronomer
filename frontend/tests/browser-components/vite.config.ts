import { readFileSync } from "node:fs";
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Separate build: the fixture and its controls never enter the operator bundle.
export default defineConfig({
  root: fileURLToPath(new URL(".", import.meta.url)),
  publicDir: fileURLToPath(new URL("../../public", import.meta.url)),
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("../../src", import.meta.url)) },
  },
  preview: {
    headers: {
      "Content-Security-Policy": readFileSync(
        new URL("../../nginx.conf", import.meta.url),
        "utf8",
      ).match(/add_header Content-Security-Policy "([^"\n]+)"/)![1],
    },
  },
  build: { outDir: "../../.cache/browser-components", emptyOutDir: true },
});
