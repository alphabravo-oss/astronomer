#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function run(script, args = []) {
  const result = spawnSync(process.execPath, [path.join(repoRoot, script), ...args], {
    cwd: repoRoot,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(
      `${script} failed while deriving the Rancher comparison contract:\n${result.stderr || result.stdout}`,
    );
  }
  return result.stdout;
}

export function checkRancherComparison() {
  const quality = run("scripts/openapi-quality.mjs");
  const coverage = run("scripts/openapi-coverage.mjs", ["--check"]);
  const requestFields = run("scripts/openapi-request-fields.mjs", ["--check"]);
  run("scripts/validate-rancher-benchmark.mjs");

  const qualityMatch = quality.match(
    /passed for (\d+) operations, including (\d+) actionable 202 mutations and (\d+) explicit exceptions/,
  );
  const coverageMatch = coverage.match(/coverage\s+:\s+100\.0%\s+\((\d+)\/(\d+)\)/);
  const bindingMatch = requestFields.match(
    /bindings \(marker occurrences\)\s+:\s+(\d+)/,
  );
  const provisionalMatch = requestFields.match(
    /explicitly provisional\s+:\s+(\d+)/,
  );
  if (!qualityMatch || !coverageMatch || !bindingMatch || !provisionalMatch) {
    throw new Error("could not parse one or more authoritative API contract reports");
  }

  const expected = [
    `operations=${qualityMatch[1]}`,
    `mounted_routes=${coverageMatch[1]}/${coverageMatch[2]}`,
    `request_bindings=${bindingMatch[1]}`,
    `provisional_request_shapes=${provisionalMatch[1]}`,
    `actionable_202=${qualityMatch[2]}`,
    `async_202_exceptions=${qualityMatch[3]}`,
  ].join(" ");
  const comparison = fs.readFileSync(
    path.join(repoRoot, "docs/rancher-astronomer-comparison.md"),
    "utf8",
  );
  const marker = comparison.match(/<!-- api-contract-evidence: ([^>]+) -->/);
  if (!marker) throw new Error("comparison document has no api-contract-evidence marker");
  if (marker[1].trim() !== expected) {
    throw new Error(
      `comparison API evidence is stale\nexpected: ${expected}\nactual:   ${marker[1].trim()}`,
    );
  }
  if (!comparison.includes("Relative clicks, clarity, recovery, and completion time remain unqualified")) {
    throw new Error("comparison must keep Rancher-relative UX explicitly unqualified");
  }
  return expected;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const expected = checkRancherComparison();
    console.log(`Rancher comparison contract passed: ${expected}`);
  } catch (error) {
    console.error(`Rancher comparison contract failed: ${(error).message}`);
    process.exit(1);
  }
}
