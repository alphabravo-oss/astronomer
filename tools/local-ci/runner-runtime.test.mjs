import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

test("installed runner dependencies load and exit cleanly on the active Node runtime", () => {
  // --help intentionally skips runner dependencies, so it cannot detect native
  // addons left behind after changing Node versions. Isolate the import: an ABI
  // mismatch can abort during teardown even when an optional import catches it.
  const entry = new URL("./node_modules/run-local-ci/dist/commands/run.js", import.meta.url);
  const result = spawnSync(process.execPath, [
    "--input-type=module", "-e", `await import(${JSON.stringify(entry.href)})`,
  ], { encoding: "utf8", timeout: 30_000 });
  assert.equal(result.status, 0,
    `Local CI dependency preflight failed (${result.signal ?? result.error ?? result.status}). ` +
    "Reinstall with the same Node runtime used to run CI: npm ci --prefix tools/local-ci.\n" +
    result.stderr);
});
