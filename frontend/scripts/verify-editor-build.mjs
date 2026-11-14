import { readFile, readdir } from "node:fs/promises";
import { createHash } from "node:crypto";
export async function verifyEditorBuild(directory) {
  const manifest = JSON.parse(
    await readFile(new URL("build.json", directory), "utf8"),
  );
  const lock = JSON.parse(
    await readFile(new URL("../package-lock.json", import.meta.url), "utf8"),
  );
  if (
    manifest.dompurify !== lock.packages["node_modules/dompurify"].version ||
    manifest.monaco !== lock.packages["node_modules/monaco-editor"].version ||
    manifest.sanitizer !== "locked-package-esm" ||
    manifest.vendoredSanitizer !== false
  ) {
    throw new Error(
      "Editor sanitizer provenance differs from locked source build",
    );
  }
  for (const required of [
    "editor.js",
    "editor.css",
    "editor.worker.js",
    "json.worker.js",
    "css.worker.js",
    "html.worker.js",
    "ts.worker.js",
  ]) {
    if (!manifest.files?.[required])
      throw new Error(`Missing editor asset: ${required}`);
  }
  const files = (await readdir(directory, { recursive: true })).filter(
    (name) => name !== "build.json",
  );
  const actual = [];
  for (const name of files) {
    if (name.includes("..") || name.startsWith("/"))
      throw new Error("Invalid editor asset path");
    let bytes;
    try {
      bytes = await readFile(new URL(name, directory));
    } catch (error) {
      if (error.code === "EISDIR") continue;
      throw error;
    }
    actual.push(name);
    if (
      createHash("sha256").update(bytes).digest("hex") !== manifest.files[name]
    )
      throw new Error(`Editor asset hash mismatch: ${name}`);
  }
  if (actual.length !== Object.keys(manifest.files).length)
    throw new Error("Editor asset inventory mismatch");
}
