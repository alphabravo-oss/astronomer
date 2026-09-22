/**
 * Mutation-feedback ratchet (P023.6).
 *
 * "Every mutation must report failure" started as an ESLint
 * `no-restricted-syntax` rule (`useMutation({...})` without an `onError`),
 * but a first pass over the codebase (after fixing the five call sites in
 * settings/widgets, the original trigger for this work) found 28 remaining
 * pre-existing sites that would each need a `// eslint-disable-next-line` —
 * comfortably past the ~15 threshold where the plan calls for a counting
 * ratchet instead. This test is that ratchet: the number of
 * `useMutation({...})` call sites with neither an `onError` handler nor an
 * explicit `// mutation-error: rendered inline` escape comment must never
 * grow. Fixing a site (adding `onError`, or the escape comment for one that
 * already renders `mutation.error` inline) should lower BASELINE; a new
 * unhandled mutation should not raise it.
 */
import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// Measured 2026-09-22 when the ratchet was added (plan 023 step 6).
const BASELINE_UNHANDLED_MUTATIONS = 28;

const ESCAPE_COMMENT = "mutation-error: rendered inline";

function walkSourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "__tests__") continue;
    const path = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walkSourceFiles(path));
    } else if (
      /\.tsx?$/.test(entry.name) &&
      !entry.name.includes(".test.") &&
      entry.name !== "routeTree.gen.ts"
    ) {
      out.push(path);
    }
  }
  return out;
}

/** Extracts each `useMutation({ ... })` call's argument object as raw text. */
function mutationBlocks(source: string): string[] {
  const blocks: string[] = [];
  const re = /useMutation\(\{/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(source))) {
    let depth = 1;
    let i = match.index + match[0].length;
    while (depth > 0 && i < source.length) {
      if (source[i] === "{") depth++;
      else if (source[i] === "}") depth--;
      i++;
    }
    blocks.push(source.slice(match.index, i));
  }
  return blocks;
}

function countUnhandledMutations(): number {
  const root = resolve(process.cwd(), "src");
  let count = 0;
  for (const file of walkSourceFiles(root)) {
    const source = readFileSync(file, "utf8");
    if (!source.includes("useMutation(")) continue;
    for (const block of mutationBlocks(source)) {
      if (!/onError/.test(block) && !source.includes(ESCAPE_COMMENT)) {
        count++;
      }
    }
  }
  return count;
}

describe("mutation feedback ratchet", () => {
  it("does not add new useMutation call sites without failure reporting", () => {
    expect(countUnhandledMutations()).toBeLessThanOrEqual(
      BASELINE_UNHANDLED_MUTATIONS,
    );
  });
});
