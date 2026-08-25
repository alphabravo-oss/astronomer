#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const hotspotCeilings = new Map([
  ["internal/server/server.go", 1200],
  ["internal/handler/resources.go", 1000],
  ["frontend/src/lib/api.ts", 220],
  ["frontend/src/lib/api/settings.ts", 100],
  ["frontend/src/lib/hooks.ts", 100],
  ["frontend/src/types/index.ts", 100],
  ["frontend/src/routes/dashboard/clusters/$id/$resource/index.tsx", 4650],
]);
const fileBudgets = new Map([
  [".go", 800],
  [".ts", 700],
  [".tsx", 1000],
  [".mjs", 700],
]);
const functionBudget = 240;

function git(args, fallback = "") {
  try {
    return execFileSync("git", args, {
      cwd: root,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return fallback;
  }
}

function excluded(file) {
  return (
    !fileBudgets.has(path.extname(file)) ||
    /(?:^|\/)(?:node_modules|vendor|dist|archive|testdata)\//.test(file) ||
    /(?:_test\.go|\.(?:test|spec)\.[cm]?[jt]sx?)$/.test(file) ||
    /(?:\.generated\.|\.gen\.|\/generated\/|routeTree\.gen\.ts$|internal\/db\/sqlc\/)/.test(file)
  );
}

function changedFiles() {
  const files = new Set();
  const explicitBase = process.env.COMPLEXITY_BASE_REF;
  const githubBase = process.env.GITHUB_BASE_REF;
  let base = explicitBase;
  if (!base && githubBase) {
    const remote = `origin/${githubBase}`;
    base = git(["merge-base", "HEAD", remote]);
  }
  if (base) {
    git(["diff", "--name-only", "--diff-filter=ACMRT", `${base}...HEAD`])
      .split(/\r?\n/)
      .filter(Boolean)
      .forEach((file) => files.add(file));
  } else {
    git(["diff", "--name-only", "--diff-filter=ACMRT", "HEAD"])
      .split(/\r?\n/)
      .filter(Boolean)
      .forEach((file) => files.add(file));
    git(["ls-files", "--others", "--exclude-standard"])
      .split(/\r?\n/)
      .filter(Boolean)
      .forEach((file) => files.add(file));
  }
  // In a dirty local worktree, compare changed files with HEAD. In CI the
  // merge-base above compares the complete pull request with its base branch.
  // Keeping the comparison ref explicit avoids treating every pre-existing
  // function as newly introduced during local verification.
  return {
    base: base || "HEAD",
    files: [...files].filter((file) => !excluded(file)).sort(),
  };
}

function sourceAt(ref, file) {
  if (!ref) return null;
  try {
    return execFileSync("git", ["show", `${ref}:${file}`], {
      cwd: root,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    });
  } catch {
    return null;
  }
}

function lineCount(source) {
  return source === "" ? 0 : source.split(/\r?\n/).length;
}

// A deliberately conservative brace-based characterization. It recognizes
// named Go functions and named TS function declarations; unknown syntax is
// ignored rather than producing a false pass/fail for the whole file.
function functionLengths(source, extension) {
  const lines = source.split(/\r?\n/);
  const starts = extension === ".go"
    ? /^func\s+(?:\([^)]*\)\s*)?([A-Za-z][A-Za-z0-9_]*)\s*\(/
    : /^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z][A-Za-z0-9_]*)\s*\(/;
  const result = new Map();
  for (let index = 0; index < lines.length; index += 1) {
    const match = starts.exec(lines[index].trimStart());
    if (!match) continue;
    let depth = 0;
    let opened = false;
    let end = index;
    for (; end < lines.length; end += 1) {
      for (const character of lines[end]) {
        if (character === "{") { depth += 1; opened = true; }
        if (character === "}") depth -= 1;
      }
      if (opened && depth <= 0) break;
    }
    result.set(match[1], end - index + 1);
    index = end;
  }
  return result;
}

const { base, files } = changedFiles();
const failures = [];
for (const [file, ceiling] of hotspotCeilings) {
  const absolute = path.join(root, file);
  if (!fs.existsSync(absolute)) {
    failures.push(`${file}: hotspot disappeared without updating its extraction contract`);
    continue;
  }
  const lines = lineCount(fs.readFileSync(absolute, "utf8"));
  if (lines > ceiling) failures.push(`${file}: ${lines} lines exceeds no-growth ceiling ${ceiling}`);
}

for (const file of files) {
  const absolute = path.join(root, file);
  if (!fs.existsSync(absolute)) continue;
  const current = fs.readFileSync(absolute, "utf8");
  const previous = sourceAt(base, file);
  const budget = fileBudgets.get(path.extname(file));
  const currentLines = lineCount(current);
  const previousLines = previous == null ? 0 : lineCount(previous);
  if (currentLines > budget && previous == null) {
    failures.push(`${file}: ${currentLines} lines exceeds ${budget} changed-code budget (previous ${previousLines})`);
  }

  const currentFunctions = functionLengths(current, path.extname(file));
  const previousFunctions = previous == null ? new Map() : functionLengths(previous, path.extname(file));
  for (const [name, length] of currentFunctions) {
    const oldLength = previousFunctions.get(name) ?? 0;
    if (length > functionBudget && oldLength === 0) {
      failures.push(`${file}:${name}: ${length} lines exceeds ${functionBudget} changed-function budget (previous ${oldLength})`);
    }
  }
}

if (failures.length) {
  console.error(`complexity budget failed (${failures.length} finding(s)):`);
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}
console.log(`complexity budget passed: ${files.length} changed production source file(s), ${hotspotCeilings.size} hotspot ceiling(s)`);
