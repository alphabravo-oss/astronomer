#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const baselinePath = path.join(
  root,
  "docs/architecture/complexity-baseline.json",
);
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
    /(?:\.generated\.|\.gen\.|\/generated\/|routeTree\.gen\.ts$|internal\/db\/sqlc\/)/.test(
      file,
    )
  );
}

function lineCount(source) {
  return source === "" ? 0 : source.split(/\r?\n/).length;
}

// A deliberately conservative brace-based characterization. It recognizes
// named Go functions and named TS function declarations; unknown syntax is
// ignored rather than producing a false pass/fail for the whole file.
function functionLengths(source, extension) {
  const lines = source.split(/\r?\n/);
  const starts =
    extension === ".go"
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
        if (character === "{") {
          depth += 1;
          opened = true;
        }
        if (character === "}") depth -= 1;
      }
      if (opened && depth <= 0) break;
    }
    result.set(match[1], end - index + 1);
    index = end;
  }
  return result;
}

function sourceFiles() {
  const files = new Set();
  git(["ls-files"])
    .split(/\r?\n/)
    .filter(Boolean)
    .forEach((file) => files.add(file));
  git(["ls-files", "--others", "--exclude-standard"])
    .split(/\r?\n/)
    .filter(Boolean)
    .forEach((file) => files.add(file));
  return [...files].filter((file) => !excluded(file)).sort();
}

function currentComplexity(files) {
  const fileCeilings = {};
  const functionCeilings = {};
  for (const file of files) {
    const absolute = path.join(root, file);
    if (!fs.existsSync(absolute)) continue;
    const source = fs.readFileSync(absolute, "utf8");
    const lines = lineCount(source);
    const fileBudget = fileBudgets.get(path.extname(file));
    if (lines > fileBudget) fileCeilings[file] = lines;

    for (const [name, length] of functionLengths(source, path.extname(file))) {
      if (length > functionBudget) {
        functionCeilings[`${file}:${name}`] = length;
      }
    }
  }
  return { fileCeilings, functionCeilings };
}

function readBaseline() {
  if (!fs.existsSync(baselinePath)) {
    throw new Error(
      `missing ${path.relative(root, baselinePath)}; run this script with --write-baseline`,
    );
  }
  const parsed = JSON.parse(fs.readFileSync(baselinePath, "utf8"));
  return {
    fileCeilings: parsed.fileCeilings ?? {},
    functionCeilings: parsed.functionCeilings ?? {},
  };
}

const files = sourceFiles();
const current = currentComplexity(files);

if (process.argv.includes("--write-baseline")) {
  fs.writeFileSync(baselinePath, `${JSON.stringify(current, null, 2)}\n`);
  console.log(
    `complexity baseline wrote ${Object.keys(current.fileCeilings).length} file ceiling(s) and ${Object.keys(current.functionCeilings).length} function ceiling(s)`,
  );
  process.exit(0);
}

const baseline = readBaseline();
const failures = [];
for (const [file, ceiling] of Object.entries(baseline.fileCeilings)) {
  const lines = current.fileCeilings[file];
  if (lines === undefined) {
    failures.push(
      `${file}: no longer exceeds its file budget; regenerate the baseline`,
    );
    continue;
  }
  if (lines > ceiling)
    failures.push(
      `${file}: ${lines} lines exceeds no-growth ceiling ${ceiling}`,
    );
  if (lines < ceiling)
    failures.push(
      `${file}: ${lines} lines is below stale ceiling ${ceiling}; lower the ratchet baseline`,
    );
}

for (const [file, lines] of Object.entries(current.fileCeilings)) {
  if (!(file in baseline.fileCeilings)) {
    failures.push(
      `${file}: ${lines} lines exceeds ${fileBudgets.get(path.extname(file))} line budget without a baseline`,
    );
  }
}

for (const [key, ceiling] of Object.entries(baseline.functionCeilings)) {
  const length = current.functionCeilings[key];
  if (length === undefined) {
    failures.push(
      `${key}: no longer exceeds its function budget; regenerate the baseline`,
    );
    continue;
  }
  if (length > ceiling)
    failures.push(
      `${key}: ${length} lines exceeds no-growth ceiling ${ceiling}`,
    );
  if (length < ceiling)
    failures.push(
      `${key}: ${length} lines is below stale ceiling ${ceiling}; lower the ratchet baseline`,
    );
}

for (const [key, length] of Object.entries(current.functionCeilings)) {
  if (!(key in baseline.functionCeilings)) {
    failures.push(
      `${key}: ${length} lines exceeds ${functionBudget} line function budget without a baseline`,
    );
  }
}

if (failures.length) {
  console.error(`complexity budget failed (${failures.length} finding(s)):`);
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}
console.log(
  `complexity budget passed: ${files.length} production source file(s), ${Object.keys(baseline.fileCeilings).length} file ceiling(s), ${Object.keys(baseline.functionCeilings).length} function ceiling(s)`,
);
