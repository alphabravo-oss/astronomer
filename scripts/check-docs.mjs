#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const docsRoot = path.join(root, "docs");
const excludedPrefixes = [
  "docs/archive/",
  "docs/plans/",
  "docs/assurance/",
];
const legacyContextAllowlist = new Set([
  "docs/architecture/decisions/flux-native-delivery.md",
  "docs/control-plane-state-contract.md",
  "docs/rancher-astronomer-comparison.md",
]);
const legacyTerms = [
  { name: "Argo CD runtime", pattern: /\b(?:Argo\s*CD|ArgoCD|argocd)\b/i },
  { name: "ApplicationSet runtime", pattern: /\bApplicationSet\b/i },
  { name: "legacy fleet operation", pattern: /\b(?:fleet_operations?|agent fleet)\b/i },
];

function walk(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...walk(file));
    else if (entry.isFile() && entry.name.endsWith(".md")) out.push(file);
  }
  return out;
}

function relative(file) {
  return path.relative(root, file).replaceAll(path.sep, "/");
}

function isCurrent(file) {
  return !excludedPrefixes.some((prefix) => file.startsWith(prefix));
}

function localLinkTarget(sourceFile, rawTarget) {
  const target = rawTarget.trim().replace(/^<|>$/g, "");
  if (
    !target ||
    target.startsWith("#") ||
    target.startsWith("/") ||
    /^[a-z][a-z0-9+.-]*:/i.test(target) ||
    target.includes("{{")
  ) {
    return null;
  }
  const withoutSuffix = target.split("#", 1)[0].split("?", 1)[0];
  if (!withoutSuffix) return null;
  let decoded;
  try {
    decoded = decodeURIComponent(withoutSuffix);
  } catch {
    return { error: "invalid percent-encoding", target };
  }
  // Generated inventories intentionally render repository-root source links
  // such as `internal/handler/foo.go:42`. Resolve those from the repository,
  // and ignore their optional editor-style line suffix for existence checks.
  const sourceStyle = /^(?:cmd|deploy|frontend|internal|pkg|scripts)\//.test(decoded);
  const withoutLine = sourceStyle ? decoded.replace(/:\d+(?::\d+)?$/, "") : decoded;
  return {
    file: sourceStyle
      ? path.resolve(root, withoutLine)
      : path.resolve(path.dirname(sourceFile), withoutLine),
    target,
  };
}

const failures = [];
let checkedLinks = 0;
let currentDocuments = 0;

for (const file of walk(docsRoot).sort()) {
  const repoFile = relative(file);
  if (!isCurrent(repoFile)) continue;
  currentDocuments += 1;
  const source = fs.readFileSync(file, "utf8");
  if (!/^#\s+\S/m.test(source)) {
    failures.push(`${repoFile}: missing level-one title`);
  }

  if (!legacyContextAllowlist.has(repoFile)) {
    const lines = source.split(/\r?\n/);
    lines.forEach((line, index) => {
      for (const term of legacyTerms) {
        if (term.pattern.test(line)) {
          failures.push(`${repoFile}:${index + 1}: stale ${term.name} terminology`);
        }
      }
    });
  }

  for (const match of source.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
    const parsed = localLinkTarget(file, match[1]);
    if (!parsed) continue;
    checkedLinks += 1;
    const line = source.slice(0, match.index).split(/\r?\n/).length;
    if (parsed.error) {
      failures.push(`${repoFile}:${line}: ${parsed.error} in ${parsed.target}`);
      continue;
    }
    if (!fs.existsSync(parsed.file)) {
      failures.push(`${repoFile}:${line}: broken relative link ${parsed.target}`);
    }
  }
}

for (const required of [
  "docs/README.md",
  "docs/architecture/compatibility.md",
  "docs/control-plane-state-contract.md",
  "docs/engineering-ownership.md",
  "docs/rancher-astronomer-comparison.md",
  "docs/runbooks/README.md",
]) {
  if (!fs.existsSync(path.join(root, required))) failures.push(`${required}: required current document is missing`);
}

const comparisonContract = spawnSync(
  process.execPath,
  [path.join(root, "scripts/check-rancher-comparison.mjs")],
  { cwd: root, encoding: "utf8" },
);
if (comparisonContract.status !== 0) {
  failures.push(
    `docs/rancher-astronomer-comparison.md: semantic contract failed: ${(
      comparisonContract.stderr || comparisonContract.stdout
    ).trim()}`,
  );
}

if (failures.length) {
  console.error(`documentation contract failed (${failures.length} finding(s)):`);
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log(`documentation contract passed: ${currentDocuments} current documents, ${checkedLinks} relative links`);
