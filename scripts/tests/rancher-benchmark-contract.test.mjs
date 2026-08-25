import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  RANCHER_DASHBOARD_VERSION,
  RANCHER_SOURCE_COMMIT,
  validateEvidence,
  validateTaskDefinition,
} from "../validate-rancher-benchmark.mjs";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const tasks = JSON.parse(
  fs.readFileSync(
    path.join(
      repoRoot,
      "docs/assurance/rancher-benchmark/tasks-v1.json",
    ),
    "utf8",
  ),
);

const subject = (commit, uiVersion) => ({
  source_commit: commit,
  image_digest: `sha256:${"a".repeat(64)}`,
  ui_version: uiVersion,
});

function evidence() {
  return {
    schema_version: "rancher-ux-benchmark/v1",
    run: {
      run_id: "unit-fixture",
      started_at: "2026-08-25T00:00:00Z",
      finished_at: "2026-08-25T00:10:00Z",
      environment: "disposable-test",
      browser: "Chromium fixture",
    },
    subjects: {
      astronomer: subject("b".repeat(40), "test"),
      rancher: subject(RANCHER_SOURCE_COMMIT, RANCHER_DASHBOARD_VERSION),
    },
    policy: {
      repetitions_per_cache_mode: 3,
      max_task_regression_ratio: 1.2,
      task_set_active_time_ratio: 1,
    },
    qualification: {
      automated: "pending",
      human_clarity: "pending",
      overall: "pending",
    },
    task_results: [
      {
        task_id: tasks.tasks[0].id,
        product: "astronomer",
        cache_mode: "cold",
        attempt: 1,
        outcome: "pass",
        metrics: {
          active_ms: 100,
          wall_ms: 120,
          pointer_activations: 2,
          keyboard_activations: 0,
          route_transitions: 1,
          error_count: 0,
          recovery_actions: 0,
          duplicate_effects: 0,
        },
        evidence: {
          trace_uri: "artifact://trace.zip",
          trace_sha256: `sha256:${"c".repeat(64)}`,
          screenshot_uri: "artifact://screenshot.png",
          screenshot_sha256: `sha256:${"d".repeat(64)}`,
          success_probe: "fixture API observed expected state",
        },
      },
    ],
  };
}

test("checked-in task contract is neutral and pinned", () => {
  assert.deepEqual(validateTaskDefinition(tasks), []);
});

test("pending partial evidence is valid but cannot claim overall pass", () => {
  const partial = evidence();
  assert.deepEqual(validateEvidence(partial, tasks), []);
  partial.qualification.overall = "pass";
  assert.match(validateEvidence(partial, tasks).join("\n"), /cannot pass/);
});

test("automated pass requires paired results for every task", () => {
  const partial = evidence();
  partial.qualification.automated = "pass";
  assert.match(validateEvidence(partial, tasks).join("\n"), /requires exactly 3 passing repetitions/);
});

test("automated pass enforces cold/warm repetitions and non-inferiority", () => {
  const complete = evidence();
  complete.task_results = [];
  for (const task of tasks.tasks) {
    for (const product of ["astronomer", "rancher"]) {
      for (const cache_mode of ["cold", "warm"]) {
        for (let attempt = 1; attempt <= 3; attempt += 1) {
          const row = structuredClone(evidence().task_results[0]);
          Object.assign(row, { task_id: task.id, product, cache_mode, attempt });
          row.metrics.active_ms = product === "astronomer" ? 100 : 110;
          complete.task_results.push(row);
        }
      }
    }
  }
  complete.qualification.automated = "pass";
  assert.deepEqual(validateEvidence(complete, tasks), []);
  complete.task_results.find((row) => row.product === "astronomer").metrics.active_ms = 1000;
  complete.task_results.filter((row) => row.product === "astronomer" && row.task_id === tasks.tasks[0].id && row.cache_mode === "cold")[1].metrics.active_ms = 1000;
  assert.match(validateEvidence(complete, tasks).join("\n"), /non-inferiority/);
});

test("credential-like fields and duplicate effects are rejected", () => {
  const invalid = evidence();
  invalid.access_token = "must-never-be-retained";
  invalid.task_results[0].metrics.duplicate_effects = 1;
  const result = validateEvidence(invalid, tasks).join("\n");
  assert.match(result, /credential-like fields are forbidden/);
  assert.match(result, /duplicate effects/);
});
