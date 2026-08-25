#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  RANCHER_DASHBOARD_VERSION,
  RANCHER_SOURCE_COMMIT,
  validateEvidence,
} from "./validate-rancher-benchmark.mjs";

const humanPath = process.argv[2];
if (!humanPath) throw new Error("usage: validate-rancher-human-study.mjs <human-evaluation.json>");
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const tasks = JSON.parse(fs.readFileSync(path.join(root, "docs/assurance/rancher-benchmark/tasks-v1.json"), "utf8"));
const human = JSON.parse(fs.readFileSync(humanPath, "utf8"));
const digest = `sha256:${"a".repeat(64)}`;
const evidence = {
  schema_version: "rancher-ux-benchmark/v1",
  run: {run_id: "human-validation", started_at: "2026-01-01T00:00:00Z", finished_at: "2026-01-01T00:00:01Z", environment: "validation", browser: "not-applicable"},
  subjects: {
    astronomer: {source_commit: "b".repeat(40), image_digest: digest, ui_version: "validation"},
    rancher: {source_commit: RANCHER_SOURCE_COMMIT, image_digest: digest, ui_version: RANCHER_DASHBOARD_VERSION},
  },
  policy: {repetitions_per_cache_mode: 3, max_task_regression_ratio: 1.2, task_set_active_time_ratio: 1},
  qualification: {automated: "pending", human_clarity: "pass", overall: "pending"},
  task_results: [{
    task_id: tasks.tasks[0].id, product: "astronomer", cache_mode: "cold", attempt: 1, outcome: "pass",
    metrics: {active_ms: 1, wall_ms: 1, pointer_activations: 0, keyboard_activations: 0, route_transitions: 0, error_count: 0, recovery_actions: 0, duplicate_effects: 0},
    evidence: {trace_uri: "artifact://validation", trace_sha256: digest, screenshot_uri: "artifact://validation", screenshot_sha256: digest, success_probe: "validation fixture"},
  }],
  human_evaluation: {...human, source_evidence_sha256: digest, source_bundle_sha256: digest, source_run_id: "1", source_workflow: ".github/workflows/rancher-human-study.yaml", source_commit: "b".repeat(40), source_ref: "refs/tags/v1.0.0", source_conclusion: "success"},
};
const errors = validateEvidence(evidence, tasks);
if (errors.length) {
  console.error(errors.join("\n"));
  process.exit(1);
}
