#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

export const coreQueryFiles = [
  "frontend/src/lib/hooks/alerting.ts",
  "frontend/src/lib/hooks/audit.ts",
  "frontend/src/lib/hooks/catalog.ts",
  "frontend/src/lib/hooks/cluster-search.ts",
  "frontend/src/lib/hooks/clusters.ts",
  "frontend/src/lib/hooks/kubernetes-proxy.ts",
  "frontend/src/lib/hooks/policy-queries.ts",
  "frontend/src/lib/hooks/projects.ts",
  "frontend/src/lib/hooks/rbac.ts",
  "frontend/src/lib/hooks/workloads.ts",
];

/** Find query functions that cannot consume TanStack Query's AbortSignal. */
export function findDroppedQuerySignals(source) {
  const findings = [];
  const unsafe =
    /queryFn:\s*(?:async\s*)?(?:\(\s*\)|[A-Za-z_$][\w$]*)\s*(?:=>|(?=,|\n|\}))/g;
  for (const match of source.matchAll(unsafe)) {
    const line = source.slice(0, match.index).split("\n").length;
    findings.push({ line, snippet: match[0].trim() });
  }
  return findings;
}

function main() {
  const failures = [];
  for (const relative of coreQueryFiles) {
    const source = fs.readFileSync(path.join(repoRoot, relative), "utf8");
    for (const finding of findDroppedQuerySignals(source)) {
      failures.push(`${relative}:${finding.line}: ${finding.snippet}`);
    }
  }
  if (failures.length > 0) {
    console.error(
      `core TanStack queries must accept and forward { signal }:\n${failures.join("\n")}`,
    );
    process.exit(1);
  }
  console.log(
    `frontend query cancellation contract passed: ${coreQueryFiles.length} core hook modules`,
  );
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main();
}
