import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const frontend = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

test("staged formatting checks index blobs, ignores generated files and never rewrites", (t) => {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "astronomer-format-test-"),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const directory = path.join(root, "frontend");
  fs.mkdirSync(path.join(directory, "scripts"), { recursive: true });
  fs.mkdirSync(path.join(directory, "src/lib/api/generated"), {
    recursive: true,
  });
  for (const file of [
    ".prettierrc.json",
    ".prettierignore",
    "scripts/check-staged-formatting.mjs",
  ]) {
    fs.copyFileSync(path.join(frontend, file), path.join(directory, file));
  }
  fs.symlinkSync(
    path.join(frontend, "node_modules"),
    path.join(directory, "node_modules"),
    "dir",
  );
  const git = (...args) =>
    execFileSync("git", args, { cwd: root, stdio: "pipe" });
  git("init", "--quiet");
  const check = () =>
    spawnSync(
      process.execPath,
      [path.join(directory, "scripts/check-staged-formatting.mjs")],
      { cwd: root, encoding: "utf8" },
    );
  const file = path.join(directory, "src/example.ts");
  const unformatted = "export const value={a:1}\n";
  const formatted = "export const value = { a: 1 };\n";
  fs.writeFileSync(file, unformatted);
  git("add", "frontend/src/example.ts");
  assert.equal(check().status, 1);
  assert.equal(fs.readFileSync(file, "utf8"), unformatted);
  fs.writeFileSync(file, formatted);
  assert.equal(
    check().status,
    1,
    "unstaged formatting must not hide an unformatted index",
  );
  git("add", "frontend/src/example.ts");
  assert.equal(check().status, 0);
  fs.writeFileSync(file, unformatted);
  fs.writeFileSync(
    path.join(directory, "src/lib/api/generated/client.ts"),
    "not valid typescript",
  );
  git("add", "frontend/src/lib/api/generated/client.ts");
  assert.equal(
    check().status,
    0,
    "check only staged source and exclude generated contracts",
  );
  assert.equal(fs.readFileSync(file, "utf8"), unformatted);
});
