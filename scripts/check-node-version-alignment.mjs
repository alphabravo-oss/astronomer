#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

export function findNodeVersionDrift({
  version,
  packageMetadata,
  dockerfile,
  workflows,
}) {
  const failures = [];
  const major = Number(version.split(".")[0]);
  const expectedEngine = `>=${version} <${major + 1}`;
  if (packageMetadata.engines?.node !== expectedEngine) {
    failures.push(
      `frontend/package.json engines.node must be ${expectedEngine}`,
    );
  }
  if (!dockerfile.includes(`FROM node:${major}-alpine@sha256:`)) {
    failures.push(
      `frontend/Dockerfile must use a digest-pinned node:${major}-alpine builder`,
    );
  }
  for (const [name, source] of workflows) {
    if (!source.includes("actions/setup-node")) continue;
    const declared = [...source.matchAll(/node-version:\s*([^,}\s]+)/g)].map(
      (match) => match[1].replaceAll(/["']/g, ""),
    );
    if (declared.length === 0 || declared.some((value) => value !== version)) {
      failures.push(`${name} setup-node entries must use ${version}`);
    }
  }
  return failures;
}

function main() {
  const version = fs
    .readFileSync(path.join(repoRoot, "frontend/.nvmrc"), "utf8")
    .trim();
  const packageMetadata = JSON.parse(
    fs.readFileSync(path.join(repoRoot, "frontend/package.json"), "utf8"),
  );
  const dockerfile = fs.readFileSync(
    path.join(repoRoot, "frontend/Dockerfile"),
    "utf8",
  );
  const workflowDir = path.join(repoRoot, ".github/workflows");
  const workflows = fs
    .readdirSync(workflowDir)
    .filter((name) => name.endsWith(".yaml") || name.endsWith(".yml"))
    .map((name) => [
      `.github/workflows/${name}`,
      fs.readFileSync(path.join(workflowDir, name), "utf8"),
    ]);
  const failures = findNodeVersionDrift({
    version,
    packageMetadata,
    dockerfile,
    workflows,
  });
  if (failures.length > 0) {
    console.error(`Node runtime alignment failed:\n${failures.join("\n")}`);
    process.exit(1);
  }
  console.log(`Node runtime alignment passed: ${version}`);
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main();
}
