// Version-bound repair for run-local-ci 0.18.1's dependency result context.
// The aggregate must receive real results, including any failed matrix entry.
export const originalCollector = `    const collectOutputs = (result, taskName) => {
        if (result.outputs && Object.keys(result.outputs).length > 0) {
            jobOutputs.set(taskName, result.outputs);
        }
    };`;

export const patchedCollector = `    const collectOutputs = (result, taskName) => {
        const previous = jobOutputs.get(taskName);
        jobOutputs.set(taskName, {
            ...previous,
            ...result.outputs,
            __result: previous?.__result === "failure" || !result.succeeded ? "failure" : "success",
        });
    };`;

// Startup exceptions are converted to failed results outside runOrSkipJob.
// Record them after the wave, and do not let a later passing matrix entry
// overwrite failure in the scheduler's separate dependency-status map.
export const originalWave = `        allResults.push(...results);
        // After each wave, resolve workflow_call outputs`;
export const patchedWave = `        for (const result of results) {
            if (!result.succeeded) {
                collectOutputs(result, result.taskId);
                jobResultStatus.set(result.taskId, "failure");
            }
        }
        allResults.push(...results);
        // After recording matrix failures, resolve workflow_call outputs`;

export const originalContext = `function resolveContextRef(trimmed, ctx) {
    if (trimmed === "runner.os") {`;

export const patchedContext = `function resolveContextRef(trimmed, ctx) {
    if (trimmed === "needs") {
        return JSON.stringify(Object.fromEntries(Object.entries(ctx.needsContext ?? {}).map(([jobId, job]) => {
            const { __result, ...outputs } = job;
            return [jobId, { result: __result ?? "failure", outputs }];
        })));
    }
    if (trimmed === "runner.os") {`;

function replaceExactly(source, original, patched, version) {
  if (version !== "0.18.1") throw new Error(`Review the needs-context patch for run-local-ci ${version}`);
  const originalCount = source.split(original).length - 1;
  const patchedCount = source.split(patched).length - 1;
  if (originalCount === 0 && patchedCount === 1) return source;
  if (originalCount !== 1 || patchedCount !== 0) {
    throw new Error("Unexpected run-local-ci dependency result implementation");
  }
  return source.replace(original, patched);
}

export function patchRunnerResults(source, version) {
  return replaceExactly(source, originalCollector, patchedCollector, version);
}

export function patchRunnerNeedsContext(source, version) {
  return replaceExactly(source, originalContext, patchedContext, version);
}

export function patchRunnerWaveResults(source, version) {
  return replaceExactly(source, originalWave, patchedWave, version);
}
