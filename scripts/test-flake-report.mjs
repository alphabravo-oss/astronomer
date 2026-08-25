#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const registryPath = path.join(root, "docs/test-quarantine.json");
const browserRoot = path.join(root, "frontend/tests");
const args = process.argv.slice(2);

function option(name) {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
}

function fail(message) {
  console.error(`test-flake-report: ${message}`);
  process.exitCode = 1;
}

function parseISODate(value, field) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value ?? "")) {
    fail(`${field} must be YYYY-MM-DD`);
    return undefined;
  }
  return new Date(`${value}T00:00:00Z`);
}

const registry = JSON.parse(fs.readFileSync(registryPath, "utf8"));
if (registry.schema_version !== 1 || !Array.isArray(registry.entries)) {
  fail("docs/test-quarantine.json has an unsupported shape");
}
const maxDays = registry.policy?.max_quarantine_days;
if (!Number.isInteger(maxDays) || maxDays < 1 || maxDays > 30) {
  fail("max_quarantine_days must be an integer from 1 through 30");
}
for (const field of ["default_retry_budget", "live_retry_budget"]) {
  const value = registry.policy?.[field];
  if (!Number.isInteger(value) || value < 0 || value > 1) {
    fail(`${field} must be zero or one`);
  }
}

const entriesByID = new Map();
const entriesByTestID = new Map();
for (const [index, entry] of registry.entries.entries()) {
  const label = `entries[${index}]`;
  for (const field of ["id", "test_id", "tier", "owner", "issue", "added_on", "expires_on", "failure_signature", "rationale"]) {
    if (typeof entry[field] !== "string" || entry[field].trim() === "") {
      fail(`${label}.${field} is required`);
    }
  }
  if (!/^[a-z0-9][a-z0-9_-]*$/.test(entry.id ?? "")) {
    fail(`${label}.id must contain only lowercase letters, digits, underscores, or hyphens`);
  }
  if (!/^https:\/\//.test(entry.issue ?? "")) fail(`${label}.issue must be an HTTPS URL`);
  if (!Number.isInteger(entry.max_retries) || entry.max_retries < 0 || entry.max_retries > 1) {
    fail(`${label}.max_retries must be zero or one`);
  }
  if (entriesByID.has(entry.id)) fail(`duplicate quarantine id ${entry.id}`);
  if (entriesByTestID.has(entry.test_id)) fail(`duplicate quarantine test_id ${entry.test_id}`);
  entriesByID.set(entry.id, entry);
  entriesByTestID.set(entry.test_id, entry);
  const added = parseISODate(entry.added_on, `${label}.added_on`);
  const expires = parseISODate(entry.expires_on, `${label}.expires_on`);
  if (added && expires) {
    const days = (expires - added) / 86_400_000;
    if (days < 0 || days > maxDays) fail(`${entry.test_id} exceeds the ${maxDays}-day quarantine limit`);
    const today = new Date();
    today.setUTCHours(0, 0, 0, 0);
    if (expires < today) fail(`${entry.test_id} quarantine expired on ${entry.expires_on}`);
  }
}

function filesUnder(directory) {
  if (!fs.existsSync(directory)) return [];
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(directory, entry.name);
    return entry.isDirectory() ? filesUnder(target) : [target];
  });
}

const referenced = new Set();
const skipPattern = /\b(?:test|it|describe)\.(?:skip|fixme)\s*\(/;
for (const file of filesUnder(browserRoot).filter((item) => /\.[cm]?[jt]sx?$/.test(item))) {
  const lines = fs.readFileSync(file, "utf8").split(/\r?\n/);
  lines.forEach((line, index) => {
    if (!skipPattern.test(line)) return;
    const marker = /QUARANTINE:([^\s*/]+)/.exec(line)?.[1];
    const relative = path.relative(root, file);
    if (!marker) {
      fail(`${relative}:${index + 1} skips a test without QUARANTINE:<id>`);
      return;
    }
    referenced.add(marker);
    if (!entriesByID.has(marker)) fail(`${relative}:${index + 1} references unknown quarantine ${marker}`);
  });
}
for (const quarantineID of entriesByID.keys()) {
  if (!referenced.has(quarantineID)) fail(`quarantine ${quarantineID} has no matching source marker`);
}

const reportOption = option("--report");
if (reportOption) {
  const reportPath = path.resolve(root, reportOption);
  const report = JSON.parse(fs.readFileSync(reportPath, "utf8"));
  const attempts = new Map();

  function normalizeFile(file) {
    if (!file) return "unknown";
    const absolute = path.isAbsolute(file) ? file : path.resolve(root, "frontend", file);
    return path.relative(root, absolute).split(path.sep).join("/");
  }

  function visitSuite(suite, inheritedFile = "", inheritedTitles = []) {
    const file = suite.file || inheritedFile;
    const titles = suite.title ? [...inheritedTitles, suite.title] : inheritedTitles;
    for (const spec of suite.specs ?? []) {
      for (const test of spec.tests ?? []) {
        const project = test.projectName || test.projectId || "default";
        const titlePath = [...titles, spec.title].filter(Boolean).join(" › ");
        const testID = `${normalizeFile(file || spec.file)} › ${titlePath} [${project}]`;
        const current = attempts.get(testID) ?? { passed: 0, failed: 0, skipped: 0, maxRetry: 0 };
        for (const result of test.results ?? []) {
          current.maxRetry = Math.max(current.maxRetry, Number(result.retry ?? 0));
          if (result.status === "passed") current.passed++;
          else if (result.status === "skipped") current.skipped++;
          else current.failed++;
        }
        if ((test.results ?? []).length === 0) {
          if (test.status === "skipped") current.skipped++;
          else if (test.status === "expected") current.passed++;
          else current.failed++;
        }
        attempts.set(testID, current);
      }
    }
    for (const child of suite.suites ?? []) visitSuite(child, file, titles);
  }
  for (const suite of report.suites ?? []) visitSuite(suite);

  const rows = [];
  let passed = 0;
  let failed = 0;
  let flaky = 0;
  let skipped = 0;
  for (const [testID, result] of [...attempts].sort(([a], [b]) => a.localeCompare(b))) {
    const isFlaky = result.passed > 0 && result.failed > 0;
    const isFailed = result.failed > 0 && result.passed === 0;
    const state = isFlaky ? "flaky" : isFailed ? "failed" : result.passed > 0 ? "passed" : "skipped";
    if (state === "flaky") flaky++;
    else if (state === "failed") failed++;
    else if (state === "passed") passed++;
    else skipped++;

    const quarantine = entriesByTestID.get(testID);
    const defaultBudget = testID.endsWith("[live]")
      ? registry.policy.live_retry_budget
      : registry.policy.default_retry_budget;
    const budget = quarantine?.max_retries ?? defaultBudget;
    if ((isFlaky || isFailed) && !quarantine) fail(`unexpected ${state} test: ${testID}`);
    if (result.maxRetry > budget) fail(`${testID} used retry ${result.maxRetry}, budget ${budget}`);
    rows.push(`| ${state} | ${result.passed} | ${result.failed} | ${result.maxRetry} | ${quarantine?.owner ?? "—"} | ${testID.replaceAll("|", "\\|")} |`);
  }

  const markdown = [
    "# Weekly test flake trend",
    "",
    `Generated: ${new Date().toISOString()}`,
    "",
    `- Passed: ${passed}`,
    `- Failed: ${failed}`,
    `- Flaky: ${flaky}`,
    `- Skipped: ${skipped}`,
    `- Active quarantines: ${entriesByID.size}`,
    "",
    "| State | Passing attempts | Failing attempts | Highest retry | Owner | Test |",
    "| --- | ---: | ---: | ---: | --- | --- |",
    ...rows,
    "",
  ].join("\n");
  const outputOption = option("--output");
  if (outputOption) fs.writeFileSync(path.resolve(root, outputOption), markdown);
  else process.stdout.write(markdown);
  if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown);
}

if (!process.exitCode) {
  console.log(`test-flake-report: policy valid (${entriesByID.size} active quarantines)`);
}
