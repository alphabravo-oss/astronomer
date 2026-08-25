#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, "..");
const contractDir = path.join(
  repoRoot,
  "docs/assurance/rancher-benchmark",
);

export const RANCHER_SOURCE_COMMIT =
  "e870de073d56eec568516fae4bc5f76c5d409909";
export const RANCHER_DASHBOARD_VERSION = "v2.15.0-alpha1";
const TASK_SCHEMA_VERSION = "rancher-ux-benchmark-tasks/v1";
const EVIDENCE_SCHEMA_VERSION = "rancher-ux-benchmark/v1";
const TASK_ID = /^[a-z0-9][a-z0-9-]{2,63}$/;
const SHA = /^[a-f0-9]{40}$/;
const DIGEST = /^sha256:[a-f0-9]{64}$/;
const REQUIRED_REPETITIONS = 3;
const NON_INFERIORITY_RATIO = 1.2;
const MIN_HUMAN_PARTICIPANTS = 8;
const MIN_HUMAN_RATING = 4;
const CREDENTIAL_KEY =
  /(?:password|passwd|bearer|authorization|session[_-]?cookie|kubeconfig|registration[_-]?credential|access[_-]?token|refresh[_-]?token|client[_-]?secret|private[_-]?key)/i;

function object(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function nonEmptyString(value) {
  return typeof value === "string" && value.trim().length > 0;
}

function safeInteger(value) {
  return Number.isSafeInteger(value) && value >= 0;
}

function median(values) {
  if (!Array.isArray(values) || values.length === 0) return Number.NaN;
  const ordered = [...values].sort((a, b) => a - b);
  const middle = Math.floor(ordered.length / 2);
  return ordered.length % 2 === 1
    ? ordered[middle]
    : (ordered[middle - 1] + ordered[middle]) / 2;
}

function onlyKeys(value, allowed, location, errors) {
  if (!object(value)) return;
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) errors.push(`${location}.${key}: unknown field`);
  }
}

function rejectCredentialFields(value, location, errors) {
  if (Array.isArray(value)) {
    value.forEach((item, index) =>
      rejectCredentialFields(item, `${location}[${index}]`, errors),
    );
    return;
  }
  if (!object(value)) return;
  for (const [key, child] of Object.entries(value)) {
    if (CREDENTIAL_KEY.test(key)) {
      errors.push(`${location}.${key}: credential-like fields are forbidden`);
    }
    rejectCredentialFields(child, `${location}.${key}`, errors);
  }
}

export function validateTaskDefinition(tasks) {
  const errors = [];
  if (!object(tasks)) return ["tasks: expected an object"];
  onlyKeys(
    tasks,
    new Set(["schema_version", "scope", "subjects", "start_state", "tasks", "claim_policy"]),
    "tasks",
    errors,
  );
  if (tasks.schema_version !== TASK_SCHEMA_VERSION) {
    errors.push(`tasks.schema_version: expected ${TASK_SCHEMA_VERSION}`);
  }
  const rancher = tasks.subjects?.rancher;
  if (rancher?.source_commit !== RANCHER_SOURCE_COMMIT) {
    errors.push("tasks.subjects.rancher.source_commit: pinned commit changed");
  }
  if (rancher?.ui_version !== RANCHER_DASHBOARD_VERSION) {
    errors.push("tasks.subjects.rancher.ui_version: pinned Dashboard changed");
  }
  if (!Array.isArray(tasks.start_state?.cache_modes)) {
    errors.push("tasks.start_state.cache_modes: expected cold/warm modes");
  } else if (
    !tasks.start_state.cache_modes.includes("cold") ||
    !tasks.start_state.cache_modes.includes("warm")
  ) {
    errors.push("tasks.start_state.cache_modes: both cold and warm are required");
  }
  if (!Array.isArray(tasks.tasks) || tasks.tasks.length < 1) {
    errors.push("tasks.tasks: at least one neutral task is required");
    return errors;
  }
  const ids = new Set();
  for (const [index, task] of tasks.tasks.entries()) {
    const at = `tasks.tasks[${index}]`;
    if (!object(task)) {
      errors.push(`${at}: expected object`);
      continue;
    }
    onlyKeys(
      task,
      new Set(["id", "prompt", "success_probe", "injection", "human_questions"]),
      at,
      errors,
    );
    if (!TASK_ID.test(task.id ?? "")) errors.push(`${at}.id: invalid task id`);
    if (ids.has(task.id)) errors.push(`${at}.id: duplicate ${task.id}`);
    ids.add(task.id);
    for (const field of ["prompt", "success_probe", "injection"]) {
      if (!nonEmptyString(task[field])) errors.push(`${at}.${field}: required`);
    }
    if (/\b(?:Astronomer|Rancher)\b/i.test(task.prompt ?? "")) {
      errors.push(`${at}.prompt: task copy must remain product-neutral`);
    }
    if (
      !Array.isArray(task.human_questions) ||
      task.human_questions.length < 1 ||
      task.human_questions.some((question) => !nonEmptyString(question))
    ) {
      errors.push(`${at}.human_questions: at least one question is required`);
    }
  }
  if (!nonEmptyString(tasks.claim_policy?.automated)) {
    errors.push("tasks.claim_policy.automated: required");
  }
  if (!nonEmptyString(tasks.claim_policy?.human)) {
    errors.push("tasks.claim_policy.human: required");
  }
  if (!nonEmptyString(tasks.claim_policy?.overall)) {
    errors.push("tasks.claim_policy.overall: required");
  }
  rejectCredentialFields(tasks, "tasks", errors);
  return errors;
}

function validateSubject(subject, at, errors) {
  if (!object(subject)) {
    errors.push(`${at}: expected object`);
    return;
  }
  onlyKeys(subject, new Set(["source_commit", "image_digest", "ui_version"]), at, errors);
  if (!SHA.test(subject.source_commit ?? "")) {
    errors.push(`${at}.source_commit: expected a 40-character commit`);
  }
  if (!DIGEST.test(subject.image_digest ?? "")) {
    errors.push(`${at}.image_digest: expected immutable sha256 digest`);
  }
  if (!nonEmptyString(subject.ui_version)) errors.push(`${at}.ui_version: required`);
}

function validateTaskResults(taskResults, expectedTaskIds, errors) {
  const resultGroups = new Map();
  if (!Array.isArray(taskResults) || taskResults.length < 1) {
    errors.push("evidence.task_results: at least one result is required");
    return resultGroups;
  }
  taskResults.forEach((result, index) => {
    const at = `evidence.task_results[${index}]`;
    if (!object(result)) {
      errors.push(`${at}: expected object`);
      return;
    }
    onlyKeys(
      result,
      new Set(["task_id", "product", "cache_mode", "attempt", "outcome", "metrics", "evidence"]),
      at,
      errors,
    );
    const metricNames = [
      "active_ms", "wall_ms", "pointer_activations", "keyboard_activations",
      "route_transitions", "error_count", "recovery_actions", "duplicate_effects",
    ];
    onlyKeys(result.metrics, new Set(metricNames), `${at}.metrics`, errors);
    onlyKeys(
      result.evidence,
      new Set(["trace_uri", "trace_sha256", "screenshot_uri", "screenshot_sha256", "success_probe"]),
      `${at}.evidence`,
      errors,
    );
    if (!expectedTaskIds.has(result.task_id)) errors.push(`${at}.task_id: unknown task`);
    if (!["astronomer", "rancher"].includes(result.product)) errors.push(`${at}.product: expected astronomer or rancher`);
    if (!["cold", "warm"].includes(result.cache_mode)) errors.push(`${at}.cache_mode: expected cold or warm`);
    if (!Number.isSafeInteger(result.attempt) || result.attempt < 1) errors.push(`${at}.attempt: expected positive integer`);
    if (!["pass", "fail"].includes(result.outcome)) errors.push(`${at}.outcome: expected pass or fail`);
    for (const metric of metricNames) {
      if (!safeInteger(result.metrics?.[metric])) errors.push(`${at}.metrics.${metric}: expected non-negative safe integer`);
    }
    if (!nonEmptyString(result.evidence?.trace_uri)) errors.push(`${at}.evidence.trace_uri: retained trace required`);
    if (!DIGEST.test(result.evidence?.trace_sha256 ?? "")) errors.push(`${at}.evidence.trace_sha256: retained trace digest required`);
    if (!nonEmptyString(result.evidence?.screenshot_uri) || !DIGEST.test(result.evidence?.screenshot_sha256 ?? "")) errors.push(`${at}.evidence: retained screenshot URI and digest required`);
    if (!nonEmptyString(result.evidence?.success_probe)) errors.push(`${at}.evidence.success_probe: independent probe required`);
    if (result.outcome === "pass" && result.metrics?.duplicate_effects !== 0) errors.push(`${at}: a passing result cannot contain duplicate effects`);
    const group = `${result.task_id}:${result.product}:${result.cache_mode}`;
    resultGroups.set(group, [...(resultGroups.get(group) ?? []), result]);
  });
  return resultGroups;
}

function validateAutomatedQualification(status, expectedTaskIds, resultGroups, errors) {
  if (status !== "pass") return;
  for (const taskId of expectedTaskIds) {
    for (const product of ["astronomer", "rancher"]) {
      for (const cacheMode of ["cold", "warm"]) {
        const group = `${taskId}:${product}:${cacheMode}`;
        const attempts = resultGroups.get(group) ?? [];
        if (attempts.length !== REQUIRED_REPETITIONS ||
            new Set(attempts.map((result) => result.attempt)).size !== REQUIRED_REPETITIONS ||
            attempts.some((result) => result.outcome !== "pass")) {
          errors.push(`evidence.qualification.automated: ${group} requires exactly ${REQUIRED_REPETITIONS} passing repetitions`);
        }
      }
    }
  }
  const astronomerTaskMedians = [];
  const rancherTaskMedians = [];
  for (const taskId of expectedTaskIds) {
    for (const cacheMode of ["cold", "warm"]) {
      const astro = resultGroups.get(`${taskId}:astronomer:${cacheMode}`) ?? [];
      const rancher = resultGroups.get(`${taskId}:rancher:${cacheMode}`) ?? [];
      if (astro.length !== REQUIRED_REPETITIONS || rancher.length !== REQUIRED_REPETITIONS) continue;
      const astroTime = median(astro.map((result) => result.metrics.active_ms));
      const rancherTime = median(rancher.map((result) => result.metrics.active_ms));
      const astroControls = median(astro.map((result) => result.metrics.pointer_activations + result.metrics.keyboard_activations));
      const rancherControls = median(rancher.map((result) => result.metrics.pointer_activations + result.metrics.keyboard_activations));
      if (astroTime > rancherTime * NON_INFERIORITY_RATIO) errors.push(`evidence.qualification.automated: ${taskId}:${cacheMode} active time exceeds non-inferiority policy`);
      if (astroControls > rancherControls * NON_INFERIORITY_RATIO) errors.push(`evidence.qualification.automated: ${taskId}:${cacheMode} control activations exceed non-inferiority policy`);
      astronomerTaskMedians.push(astroTime);
      rancherTaskMedians.push(rancherTime);
    }
  }
  if (astronomerTaskMedians.length > 0 && median(astronomerTaskMedians) > median(rancherTaskMedians)) {
    errors.push("evidence.qualification.automated: Astronomer task-set median active time is worse than Rancher");
  }
}

function validateHumanEvaluation(human, tasks, expectedTaskIds, errors) {
  if (!object(human)) return;
  onlyKeys(
    human,
    new Set(["study_version", "participant_count", "counterbalanced", "results_uri", "results_sha256", "source_evidence_sha256", "source_bundle_sha256", "source_run_id", "source_workflow", "source_commit", "source_ref", "source_conclusion", "reviewer", "responses"]),
    "evidence.human_evaluation",
    errors,
  );
  if (human.study_version !== "counterbalanced-crossover/v1") errors.push("evidence.human_evaluation.study_version: unsupported study protocol");
  const responses = human.responses;
  const participants = new Set();
  const humanGroups = new Map();
  if (Array.isArray(responses)) responses.forEach((response, index) => {
    const at = `evidence.human_evaluation.responses[${index}]`;
    onlyKeys(response, new Set(["participant_id_sha256", "task_id", "product", "order", "unassisted_completion", "first_click_correct", "help_requests", "recovery_success", "ratings"]), at, errors);
    if (!DIGEST.test(response?.participant_id_sha256 ?? "")) errors.push(`${at}.participant_id_sha256: digest required`);
    if (!expectedTaskIds.has(response?.task_id)) errors.push(`${at}.task_id: unknown task`);
    if (!["astronomer", "rancher"].includes(response?.product)) errors.push(`${at}.product: invalid product`);
    if (![1, 2].includes(response?.order)) errors.push(`${at}.order: expected 1 or 2`);
    if (typeof response?.unassisted_completion !== "boolean" || typeof response?.first_click_correct !== "boolean" || typeof response?.recovery_success !== "boolean") errors.push(`${at}: boolean outcomes required`);
    if (!safeInteger(response?.help_requests)) errors.push(`${at}.help_requests: non-negative integer required`);
    const questionCount = (tasks.tasks.find((task) => task.id === response?.task_id)?.human_questions ?? []).length;
    if (!Array.isArray(response?.ratings) || response.ratings.length !== questionCount || response.ratings.some((rating) => !Number.isInteger(rating) || rating < 1 || rating > 5)) errors.push(`${at}.ratings: one 1-5 answer per preregistered question required`);
    participants.add(response?.participant_id_sha256);
    const group = `${response?.participant_id_sha256}:${response?.task_id}`;
    humanGroups.set(group, [...(humanGroups.get(group) ?? []), response]);
  });
  if (participants.size !== human.participant_count) errors.push("evidence.human_evaluation: participant count does not match retained responses");
  for (const participant of participants) for (const taskId of expectedTaskIds) {
    const pair = humanGroups.get(`${participant}:${taskId}`) ?? [];
    if (pair.length !== 2 || new Set(pair.map((item) => item.product)).size !== 2 || new Set(pair.map((item) => item.order)).size !== 2) errors.push(`evidence.human_evaluation: missing counterbalanced product pair for ${taskId}`);
  }
  const astroRatings = (responses ?? []).filter((item) => item.product === "astronomer").flatMap((item) => item.ratings ?? []);
  const rancherRatings = (responses ?? []).filter((item) => item.product === "rancher").flatMap((item) => item.ratings ?? []);
  if ((responses ?? []).some((item) => item.product === "astronomer" && (!item.unassisted_completion || !item.recovery_success)) || median(astroRatings) < MIN_HUMAN_RATING || median(astroRatings) < median(rancherRatings) - 0.5) errors.push("evidence.qualification.human_clarity: retained outcomes do not meet the preregistered clarity policy");
}

function validateHumanQualification(status, human, errors) {
  if (status !== "pass") return;
  if (!object(human) || !Number.isSafeInteger(human.participant_count) || human.participant_count < MIN_HUMAN_PARTICIPANTS ||
      human.counterbalanced !== true || !nonEmptyString(human.results_uri) || !DIGEST.test(human.results_sha256 ?? "") ||
      !DIGEST.test(human.source_evidence_sha256 ?? "") || !DIGEST.test(human.source_bundle_sha256 ?? "") ||
      !/^\d+$/.test(human.source_run_id ?? "") || human.source_workflow !== ".github/workflows/rancher-human-study.yaml" ||
      !SHA.test(human.source_commit ?? "") || !/^refs\/tags\/v1\.[0-9]+\.[0-9]+$/.test(human.source_ref ?? "") ||
      human.source_conclusion !== "success" || !nonEmptyString(human.reviewer) || !Array.isArray(human.responses)) {
    errors.push("evidence.human_evaluation: passing clarity requires counterbalanced retained results");
  }
}

export function validateEvidence(evidence, tasks) {
  const errors = [];
  if (!object(evidence)) return ["evidence: expected an object"];
  onlyKeys(
    evidence,
    new Set([
      "schema_version",
      "run",
      "subjects",
      "qualification",
      "policy",
      "task_results",
      "human_evaluation",
    ]),
    "evidence",
    errors,
  );
  if (evidence.schema_version !== EVIDENCE_SCHEMA_VERSION) {
    errors.push(`evidence.schema_version: expected ${EVIDENCE_SCHEMA_VERSION}`);
  }
  validateSubject(evidence.subjects?.astronomer, "evidence.subjects.astronomer", errors);
  validateSubject(evidence.subjects?.rancher, "evidence.subjects.rancher", errors);
  if (evidence.subjects?.rancher?.source_commit !== RANCHER_SOURCE_COMMIT) {
    errors.push("evidence.subjects.rancher.source_commit: pinned commit changed");
  }
  if (evidence.subjects?.rancher?.ui_version !== RANCHER_DASHBOARD_VERSION) {
    errors.push("evidence.subjects.rancher.ui_version: pinned Dashboard changed");
  }
  for (const field of ["run_id", "started_at", "finished_at", "environment", "browser"]) {
    if (!nonEmptyString(evidence.run?.[field])) errors.push(`evidence.run.${field}: required`);
  }
  onlyKeys(
    evidence.run,
    new Set(["run_id", "started_at", "finished_at", "environment", "browser"]),
    "evidence.run",
    errors,
  );
  onlyKeys(
    evidence.subjects,
    new Set(["astronomer", "rancher"]),
    "evidence.subjects",
    errors,
  );
  const started = Date.parse(evidence.run?.started_at ?? "");
  const finished = Date.parse(evidence.run?.finished_at ?? "");
  if (!Number.isFinite(started) || !Number.isFinite(finished) || finished < started) {
    errors.push("evidence.run: timestamps must be ordered RFC3339 values");
  }
  onlyKeys(
    evidence.policy,
    new Set(["repetitions_per_cache_mode", "max_task_regression_ratio", "task_set_active_time_ratio"]),
    "evidence.policy",
    errors,
  );
  if (evidence.policy?.repetitions_per_cache_mode !== REQUIRED_REPETITIONS) {
    errors.push(`evidence.policy.repetitions_per_cache_mode: expected ${REQUIRED_REPETITIONS}`);
  }
  if (evidence.policy?.max_task_regression_ratio !== NON_INFERIORITY_RATIO) {
    errors.push(`evidence.policy.max_task_regression_ratio: expected ${NON_INFERIORITY_RATIO}`);
  }
  if (evidence.policy?.task_set_active_time_ratio !== 1) {
    errors.push("evidence.policy.task_set_active_time_ratio: expected 1");
  }

  const expectedTaskIds = new Set((tasks.tasks ?? []).map((task) => task.id));
  const resultGroups = validateTaskResults(evidence.task_results, expectedTaskIds, errors);

  const statuses = evidence.qualification ?? {};
  onlyKeys(
    statuses,
    new Set(["automated", "human_clarity", "overall"]),
    "evidence.qualification",
    errors,
  );
  for (const field of ["automated", "human_clarity", "overall"]) {
    if (!new Set(["pass", "fail", "pending"]).has(statuses[field])) {
      errors.push(`evidence.qualification.${field}: invalid status`);
    }
  }
  validateAutomatedQualification(statuses.automated, expectedTaskIds, resultGroups, errors);
  validateHumanQualification(statuses.human_clarity, evidence.human_evaluation, errors);
  validateHumanEvaluation(evidence.human_evaluation, tasks, expectedTaskIds, errors);
  if (
    statuses.overall === "pass" &&
    (statuses.automated !== "pass" || statuses.human_clarity !== "pass")
  ) {
    errors.push(
      "evidence.qualification.overall: cannot pass before automated and human dimensions pass",
    );
  }
  rejectCredentialFields(evidence, "evidence", errors);
  return errors;
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function cliArgument(name) {
  const index = process.argv.indexOf(name);
  return index === -1 ? undefined : process.argv[index + 1];
}

function main() {
  const schemaPath = path.join(contractDir, "benchmark-v1.schema.json");
  const tasksPath = path.join(contractDir, "tasks-v1.json");
  const schema = readJSON(schemaPath);
  const tasks = readJSON(tasksPath);
  const errors = [];
  if (schema?.properties?.schema_version?.const !== EVIDENCE_SCHEMA_VERSION) {
    errors.push("benchmark-v1.schema.json: schema-version const drifted");
  }
  errors.push(...validateTaskDefinition(tasks));

  const evidenceArg = cliArgument("--evidence");
  if (evidenceArg) {
    const evidencePath = path.resolve(process.cwd(), evidenceArg);
    errors.push(...validateEvidence(readJSON(evidencePath), tasks));
  }
  if (errors.length > 0) {
    console.error(`Rancher UX benchmark contract failed (${errors.length} finding(s)):`);
    errors.forEach((error) => console.error(`- ${error}`));
    process.exit(1);
  }
  console.log(
    `Rancher UX benchmark contract passed: ${tasks.tasks.length} neutral tasks, pinned Rancher ${RANCHER_SOURCE_COMMIT} / Dashboard ${RANCHER_DASHBOARD_VERSION}${
      evidenceArg ? ", evidence qualified honestly" : ", no comparative pass claimed"
    }`,
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
