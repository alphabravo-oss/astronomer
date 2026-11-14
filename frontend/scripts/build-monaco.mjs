import { createHash } from "node:crypto";
import { lockedSanitizerPlugin } from "./monaco-sanitizer-plugin.mjs";
import { build } from "vite";
import { readFile, writeFile, mkdir, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const path = (relative) => fileURLToPath(new URL(relative, import.meta.url));
const monaco = JSON.parse(
  await readFile(
    new URL("../node_modules/monaco-editor/package.json", import.meta.url),
    "utf8",
  ),
);
const purify = JSON.parse(
  await readFile(
    new URL("../node_modules/dompurify/package.json", import.meta.url),
    "utf8",
  ),
);

// WORKAROUND: https://github.com/microsoft/monaco-editor/issues/5517
// Rebuild Monaco ESM with the locked sanitizer instead of its vendored copy.
await build({
  configFile: false,
  base: "/monaco/",
  publicDir: false,
  plugins: [lockedSanitizerPlugin()],
  build: {
    outDir: path("../public/monaco"),
    emptyOutDir: true,
    assetsInlineLimit: 0,
    cssCodeSplit: false,
    rollupOptions: {
      input: {
        editor: path(
          "../node_modules/monaco-editor/esm/vs/editor/editor.main.js",
        ),
        "editor.worker": path(
          "../node_modules/monaco-editor/esm/vs/editor/editor.worker.js",
        ),
        "json.worker": path(
          "../node_modules/monaco-editor/esm/vs/language/json/json.worker.js",
        ),
        "css.worker": path(
          "../node_modules/monaco-editor/esm/vs/language/css/css.worker.js",
        ),
        "html.worker": path(
          "../node_modules/monaco-editor/esm/vs/language/html/html.worker.js",
        ),
        "ts.worker": path(
          "../node_modules/monaco-editor/esm/vs/language/typescript/ts.worker.js",
        ),
      },
      preserveEntrySignatures: "strict",
      output: {
        format: "es",
        entryFileNames: "[name].js",
        assetFileNames: (asset) =>
          asset.names.some((name) => name.endsWith(".css"))
            ? "editor.css"
            : "assets/[name]-[hash][extname]",
      },
    },
  },
});
await mkdir(path("../public/monaco"), { recursive: true });
const hashes = {};
for (const entry of await readdir(path("../public/monaco"), {
  recursive: true,
  withFileTypes: true,
})) {
  if (!entry.isFile()) continue;
  const full = `${entry.parentPath}/${entry.name}`;
  const relative = full.slice(path("../public/monaco").length + 1);
  hashes[relative] = createHash("sha256")
    .update(await readFile(full))
    .digest("hex");
}
await writeFile(
  path("../public/monaco/build.json"),
  JSON.stringify({
    files: hashes,
    monaco: monaco.version,
    dompurify: purify.version,
    sanitizer: "locked-package-esm",
    vendoredSanitizer: false,
  }) + "\n",
);
