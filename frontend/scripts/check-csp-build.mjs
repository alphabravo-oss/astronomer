import { access, readFile, readdir } from "node:fs/promises";

const distDirectory = new URL("../dist/", import.meta.url);

async function filesBelow(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const url = new URL(
        entry.name + (entry.isDirectory() ? "/" : ""),
        directory,
      );
      return entry.isDirectory() ? filesBelow(url) : [url];
    }),
  );
  return files.flat();
}

const index = await readFile(new URL("index.html", distDirectory), "utf8");
if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(index)) {
  throw new Error("dist/index.html contains an inline script blocked by CSP");
}

await Promise.all([
  access(new URL("theme-bootstrap.js", distDirectory)),
  access(new URL("wterm.wasm", distDirectory)),
  access(new URL("monaco/vs/loader.js", distDirectory)),
]);

const textAssets = (await filesBelow(distDirectory)).filter((url) =>
  /\.(?:css|html|js)$/.test(url.pathname),
);
let hasSameOriginMonacoConfig = false;
for (const asset of textAssets) {
  const content = await readFile(asset, "utf8");
  hasSameOriginMonacoConfig ||= content.includes("/monaco/vs");
  if (content.includes("data:font/")) {
    throw new Error(`${asset.pathname} embeds a font blocked by CSP`);
  }
}
if (!hasSameOriginMonacoConfig) {
  throw new Error(
    "built frontend does not configure Monaco for same-origin assets",
  );
}

const nginx = await readFile(new URL("../nginx.conf", import.meta.url), "utf8");
const scriptSources = nginx.match(/script-src ([^;]+);/)?.[1] ?? "";
if (!scriptSources.includes("'wasm-unsafe-eval'")) {
  throw new Error("frontend CSP does not permit WebAssembly compilation");
}
if (/\s'unsafe-(?:inline|eval)'(?:\s|$)/.test(scriptSources)) {
  throw new Error(
    "frontend script-src is broader than the CSP-safe runtime needs",
  );
}
if (!nginx.includes("worker-src 'self' blob:")) {
  throw new Error("frontend CSP does not permit same-origin Monaco workers");
}
