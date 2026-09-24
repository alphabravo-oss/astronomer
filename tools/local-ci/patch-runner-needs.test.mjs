import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { runInNewContext } from "node:vm";
import { test } from "node:test";
import {
  originalCollector, patchedCollector, originalContext, patchedContext,
  originalWave, patchedWave, patchRunnerResults, patchRunnerNeedsContext, patchRunnerWaveResults,
} from "./patch-runner-needs.mjs";
import { expandExpressions, parseWorkflowSteps } from "./node_modules/run-local-ci/dist/workflow/workflow-parser.js";

test("dependency patches reject changed versions and ambiguous implementations", () => {
  for (const [patch, original, patched] of [
    [patchRunnerResults, originalCollector, patchedCollector],
    [patchRunnerNeedsContext, originalContext, patchedContext],
    [patchRunnerWaveResults, originalWave, patchedWave],
  ]) {
    const source = `before\n${original}\nafter`;
    const changed = patch(source, "0.18.1");
    assert.equal(changed, `before\n${patched}\nafter`);
    assert.equal(patch(changed, "0.18.1"), changed);
    assert.throws(() => patch(source, "0.18.2"), /Review/);
    for (const invalid of ["", source + source, source + changed]) {
      assert.throws(() => patch(invalid, "0.18.1"), /Unexpected/);
    }
  }
});

test("real result collection retains matrix failures regardless of completion order", () => {
  const source = readFileSync(new URL("./node_modules/run-local-ci/dist/commands/run.js", import.meta.url), "utf8");
  assert.equal(source.split(patchedCollector).length - 1, 1, "install the exact results patch first");
  for (const entries of [[true, false], [false, true], [true, true]]) {
    const outputs = new Map();
    const collect = runInNewContext(`${patchedCollector}\ncollectOutputs`, { jobOutputs: outputs });
    for (const succeeded of entries) collect({ succeeded, outputs: { report: "retained" } }, "matrix");
    assert.equal(outputs.get("matrix").__result, entries.every(Boolean) ? "success" : "failure");
    assert.equal(outputs.get("matrix").report, "retained");
    collect({}, "missing-result");
    assert.equal(outputs.get("missing-result").__result, "failure");
  }
});

test("wave completion records startup failures and preserves failed scheduler state", () => {
  const source = readFileSync(new URL("./node_modules/run-local-ci/dist/commands/run.js", import.meta.url), "utf8");
  assert.equal(source.split(patchedWave).length - 1, 1, "install the exact wave patch first");
  const jobOutputs = new Map([["matrix", { __result: "success" }]]);
  const jobResultStatus = new Map([["matrix", "success"]]);
  const allResults = [];
  const results = [
    { taskId: "matrix", succeeded: false, failedStep: "[Job startup failed]" },
    { taskId: "matrix", succeeded: true },
  ];
  runInNewContext(`${patchedCollector}\n${patchedWave}`, { jobOutputs, jobResultStatus, results, allResults });
  assert.equal(jobOutputs.get("matrix").__result, "failure");
  assert.equal(jobResultStatus.get("matrix"), "failure");
  assert.equal(allResults.length, 2);
});

test("toJSON(needs) exposes actual results and outputs, with missing results failing closed", () => {
  const needs = {
    backend: { __result: "success", evidence: "sha256:fixture" },
    matrix: { __result: "failure" },
    cancelled: { __result: "cancelled" },
    skipped: { __result: "skipped" },
    unknown: {},
  };
  const json = expandExpressions("${{ toJSON(needs) }}", process.cwd(), {}, undefined, needs);
  assert.deepEqual(JSON.parse(json), {
    backend: { result: "success", outputs: { evidence: "sha256:fixture" } },
    matrix: { result: "failure", outputs: {} },
    cancelled: { result: "cancelled", outputs: {} },
    skipped: { result: "skipped", outputs: {} },
    unknown: { result: "failure", outputs: {} },
  });
  assert.equal(expandExpressions("${{ needs.backend.outputs.evidence }}", process.cwd(), {}, undefined, needs), "sha256:fixture");
});

test("the unchanged PR aggregate receives every real dependency result", async () => {
  const needs = Object.fromEntries([
    "backend", "stateful-qualifications", "postgres-failover-certification", "frontend",
    "frontend-e2e", "frontend-e2e-live", "helm", "container-supply-chain",
  ].map((name) => [name, { __result: "success" }]));
  const workflow = new URL("../../.github/workflows/pr-validation.yaml", import.meta.url).pathname;
  for (const status of ["success", "failure"]) {
    needs["stateful-qualifications"].__result = status;
    const steps = await parseWorkflowSteps(workflow, "enterprise-signoff", {}, undefined, needs);
    const aggregate = steps.find((step) => step.Name === "Require every same-commit qualification");
    const results = JSON.parse(aggregate.Env.RESULTS_JSON);
    assert.deepEqual(Object.keys(results).sort(), Object.keys(needs).sort());
    assert.equal(results["stateful-qualifications"].result, status);
    assert.equal(results.backend.result, "success");
    const execution = spawnSync("bash", ["-e", "-c", aggregate.Inputs.script], {
      encoding: "utf8", env: { PATH: process.env.PATH, RESULTS_JSON: aggregate.Env.RESULTS_JSON },
    });
    assert.equal(execution.signal, null);
    assert.equal(execution.status, status === "success" ? 0 : 1, execution.stderr);
  }
});
