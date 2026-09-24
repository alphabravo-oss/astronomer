import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, mkdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import { test } from "node:test";
import { originalCommand, patchedCommand, patchRunnerSource, runnerVersion } from "./apply-runner-patch.mjs";

test("the pinned patch is exact and idempotent, with unexpected sources rejected", () => {
  const source = `before ${originalCommand} after`;
  const patched = patchRunnerSource(source, runnerVersion);
  assert.equal(patched, `before ${patchedCommand} after`);
  assert.equal(patchRunnerSource(patched, runnerVersion), patched);
  assert.throws(() => patchRunnerSource(source, "0.18.2"), /Review/);
  for (const unexpected of ["", source + source, source + patched]) {
    assert.throws(() => patchRunnerSource(unexpected, runnerVersion), /Unexpected/);
  }
});

test("workspace permissions preserve executable identity and allow generator recreation", () => {
  const root = mkdtempSync(join(tmpdir(), "astronomer-local-ci-modes-"));
  try {
    const workspace = join(root, "work");
    const diagnostics = join(root, "diag");
    mkdirSync(workspace); mkdirSync(diagnostics);
    const source = join(workspace, "source.txt");
    const script = join(workspace, "script.sh");
    writeFileSync(source, "source"); writeFileSync(script, "#!/bin/sh\n");
    chmodSync(source, 0o644); chmodSync(script, 0o755);
    execFileSync("chmod", ["-R", "a+rwX", workspace, diagnostics]);
    assert.equal(statSync(source).mode & 0o111, 0);
    assert.equal(statSync(script).mode & 0o111, 0o111);
    assert.equal(statSync(workspace).mode & 0o777, 0o777);
    assert.equal(statSync(source).mode & 0o222, 0o222);
    // A generator replaces a non-executable file with the same Git mode.
    rmSync(source); writeFileSync(source, "source", { mode: 0o644 });
    assert.equal(statSync(source).mode & 0o111, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
