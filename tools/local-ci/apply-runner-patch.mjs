// Version-bound compatibility repairs for run-local-ci 0.18.1.
// Keep the canonical source hash sensitive to executable bits. The runner must
// make its private copy writable without turning every source file executable.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { patchRunnerNeedsContext, patchRunnerResults, patchRunnerWaveResults } from "./patch-runner-needs.mjs";

export const runnerVersion = "0.18.1";
export const originalCommand = 'chmod -R 777 "${dirs.containerWorkDir}" "${dirs.diagDir}"';
export const patchedCommand = 'chmod -R a+rwX "${dirs.containerWorkDir}" "${dirs.diagDir}"';

export function patchRunnerSource(source, version) {
  if (version !== runnerVersion) {
    throw new Error(`Review the workspace-mode patch for run-local-ci ${version}`);
  }
  const originalCount = source.split(originalCommand).length - 1;
  const patchedCount = source.split(patchedCommand).length - 1;
  if (originalCount === 0 && patchedCount === 1) return source;
  if (originalCount !== 1 || patchedCount !== 0) {
    throw new Error("Unexpected run-local-ci workspace permission implementation");
  }
  return source.replace(originalCommand, patchedCommand);
}

function installPatch() {
  const dependency = new URL("./node_modules/run-local-ci/", import.meta.url);
  const metadata = JSON.parse(readFileSync(new URL("package.json", dependency), "utf8"));
  const patches = [
    ["dist/runner/local-job.js", patchRunnerSource],
    ["dist/commands/run.js", (source, version) => patchRunnerWaveResults(patchRunnerResults(source, version), version)],
    ["dist/workflow/workflow-parser.js", patchRunnerNeedsContext],
  ].map(([path, patch]) => {
    const target = new URL(path, dependency);
    const source = readFileSync(target, "utf8");
    return { target, source, patched: patch(source, metadata.version) };
  });
  // Validate every version-bound anchor before changing any dependency file.
  for (const { target, source, patched } of patches) {
    if (patched !== source) writeFileSync(target, patched);
  }
  console.log(`Local CI ${metadata.version}: workspace modes and dependency results preserved`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  installPatch();
}
