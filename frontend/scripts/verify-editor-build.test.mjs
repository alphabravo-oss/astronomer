import { test } from "node:test";
import assert from "node:assert/strict";
import { cp, mkdtemp, readFile, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { verifyEditorBuild } from "./verify-editor-build.mjs";

test("editor provenance gate rejects changed bytes, missing inventory and stale sanitizer", async (t) => {
  const temp = await mkdtemp(join(tmpdir(), "editor-provenance-"));
  t.after(() => rm(temp, { recursive: true, force: true }));
  await cp(new URL("../public/monaco/", import.meta.url), temp, {
    recursive: true,
  });
  const dir = pathToFileURL(temp + "/");
  await verifyEditorBuild(dir);
  const entry = new URL("editor.js", dir);
  const original = await readFile(entry);
  await writeFile(
    entry,
    Buffer.concat([original, Buffer.from("/* changed */")]),
  );
  await assert.rejects(verifyEditorBuild(dir), /hash mismatch/);
  await writeFile(entry, original);
  const file = new URL("build.json", dir);
  const manifest = JSON.parse(await readFile(file, "utf8"));
  await writeFile(file, JSON.stringify({ ...manifest, dompurify: "3.4.15" }));
  await assert.rejects(verifyEditorBuild(dir), /provenance/);
  delete manifest.files["json.worker.js"];
  await writeFile(file, JSON.stringify(manifest));
  await assert.rejects(verifyEditorBuild(dir), /Missing editor asset/);
});
