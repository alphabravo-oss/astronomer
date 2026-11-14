import assert from "node:assert/strict";
import { test } from "node:test";
import { JSDOM } from "jsdom";

import { build } from "vite";
import { fileURLToPath } from "node:url";
import { lockedSanitizerPlugin } from "./monaco-sanitizer-plugin.mjs";
// Build the real Monaco sanitizer through the same guarded resolver as production.
const dom = new JSDOM("<!doctype html><html><body></body></html>");
for (const name of [
  "window",
  "document",
  "customElements",
  "MutationObserver",
  "Node",
  "HTMLElement",
  "HTMLAnchorElement",
  "HTMLImageElement",
  "HTMLInputElement",
  "DocumentFragment",
  "DOMParser",
  "Element",
]) {
  if (dom.window[name]) globalThis[name] = dom.window[name];
}
const built = await build({
  configFile: false,
  publicDir: false,
  logLevel: "silent",
  plugins: [lockedSanitizerPlugin()],
  build: {
    write: false,
    lib: {
      entry: fileURLToPath(
        new URL(
          "../node_modules/monaco-editor/esm/vs/base/browser/domSanitize.js",
          import.meta.url,
        ),
      ),
      formats: ["es"],
    },
  },
});
const code = built[0].output.find(
  (entry) => entry.type === "chunk" && entry.isEntry,
).code;
const { sanitizeHtml } = await import(
  `data:text/javascript;base64,${Buffer.from(code).toString("base64")}`
);

test("Monaco removes executable tags, handlers and dangerous link protocols", () => {
  for (const input of [
    '<img src="x" onerror="alert(1)"><script>alert(1)</script>',
    '<a href="javascript:alert(1)">unsafe link</a>',
    '<svg><a xlink:href="javascript:alert(1)">unsafe SVG</a></svg>',
    '<math><mtext><img src="x" onerror="alert(1)"></mtext></math>',
  ]) {
    const output = document.createElement("div");
    output.innerHTML = String(sanitizeHtml(input));
    assert.equal(output.querySelector("script,svg,math,iframe,object"), null);
    for (const element of output.querySelectorAll("*")) {
      for (const attribute of element.attributes) {
        assert.ok(!attribute.name.startsWith("on"));
        assert.ok(!attribute.value.toLowerCase().includes("javascript:"));
      }
    }
  }
});

test("Monaco preserves permitted markup and HTTPS links", () => {
  const output = document.createElement("div");
  output.innerHTML = String(
    sanitizeHtml(
      '<strong>Safe</strong> <a href="https://example.com">link</a>',
    ),
  );
  assert.equal(output.querySelector("strong")?.textContent, "Safe");
  assert.equal(
    output.querySelector("a")?.getAttribute("href"),
    "https://example.com",
  );
});
