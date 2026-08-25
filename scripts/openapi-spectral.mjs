#!/usr/bin/env node

import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const spectral = path.join(root, "frontend", "node_modules", ".bin", "spectral");
const spec = path.join(root, "docs", "openapi.yaml");
const ruleset = path.join(root, ".spectral.yaml");

const result = spawnSync(
  spectral,
  ["lint", spec, "--ruleset", ruleset, "--format", "json", "--fail-severity", "error"],
  { cwd: root, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 },
);
if (result.error) {
  console.error(`OpenAPI Spectral execution failed: ${result.error.message}`);
  process.exit(2);
}

let diagnostics;
try {
  diagnostics = JSON.parse(result.stdout || "[]");
} catch (error) {
  console.error("OpenAPI Spectral returned invalid JSON.");
  console.error(result.stderr.trim());
  process.exit(2);
}
const errors = diagnostics.filter((diagnostic) => diagnostic.severity === 0);
if (errors.length > 0) {
  console.error(`OpenAPI Spectral schema lint failed with ${errors.length} error(s):`);
  for (const error of errors) {
    const location = error.range?.start
      ? `${error.range.start.line + 1}:${error.range.start.character + 1}`
      : "unknown";
    console.error(`  - ${location} ${error.code}: ${error.message}`);
  }
  process.exit(1);
}
if (result.status !== 0 && !Array.isArray(diagnostics)) {
  console.error(result.stderr.trim());
  process.exit(result.status || 2);
}
console.log(`OpenAPI Spectral schema lint passed (${diagnostics.length} advisory warning(s) suppressed).`);
