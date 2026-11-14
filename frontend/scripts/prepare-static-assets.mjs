import { copyFile, mkdir, readFile } from "node:fs/promises";

const publicDirectory = new URL("../public/", import.meta.url);
const wtermSource = new URL(
  "../node_modules/@wterm/core/wasm/wterm.wasm",
  import.meta.url,
);
const wtermDestination = new URL("wterm.wasm", publicDirectory);

await mkdir(publicDirectory, { recursive: true });
const wtermSourceContents = await readFile(wtermSource);
const wtermDestinationContents = await readFile(wtermDestination).catch(
  (error) => {
    if (error.code === "ENOENT") {
      return undefined;
    }
    throw error;
  },
);
if (!wtermDestinationContents?.equals(wtermSourceContents)) {
  await copyFile(wtermSource, wtermDestination);
}

// Build the supported ESM sources with the locked patched sanitizer; never
// copy Monaco's prebuilt AMD assets containing its independently vendored copy.
await import("./build-monaco.mjs");
