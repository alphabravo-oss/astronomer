#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const contractPath = path.join(
  repository,
  "docs/architecture/dependency-boundaries.json",
);
const contract = JSON.parse(fs.readFileSync(contractPath, "utf8"));
const failures = [];
let filesChecked = 0;
let importsChecked = 0;

function relative(file) {
  return path.relative(repository, file).replaceAll(path.sep, "/");
}

function walk(directory, extensions) {
  if (!fs.existsSync(directory)) {
    failures.push(`${relative(directory)}: configured boundary root is missing`);
    return [];
  }
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...walk(target, extensions));
    } else if (
      entry.isFile() &&
      extensions.some((extension) => entry.name.endsWith(extension))
    ) {
      files.push(target);
    }
  }
  return files;
}

function goImports(source) {
  const imports = [];
  for (const match of source.matchAll(
    /\bimport\s*(?:\(([\s\S]*?)\)|(?:[A-Za-z_.][A-Za-z0-9_.]*\s+)?"([^"]+)")/g,
  )) {
    if (match[2]) {
      imports.push(match[2]);
      continue;
    }
    for (const item of match[1].matchAll(
      /(?:^|\n)\s*(?:[A-Za-z_.][A-Za-z0-9_.]*\s+)?"([^"]+)"/g,
    )) {
      imports.push(item[1]);
    }
  }
  return imports;
}

function frontendImports(source) {
  const imports = [];
  for (const match of source.matchAll(
    /(?:\bfrom\s+|\bimport\s*\(|\bimport\s+)["']([^"']+)["']/g,
  )) {
    imports.push(match[1]);
  }
  return imports;
}

function excluded(file, rule) {
  return (rule.exclude_suffixes || []).some((suffix) => file.endsWith(suffix));
}

function violates(importPath, rule) {
  const forbidden = rule.forbidden_prefixes.some((prefix) =>
    importPath.startsWith(prefix),
  );
  const allowed = (rule.allowed_prefixes || []).some((prefix) =>
    importPath.startsWith(prefix),
  );
  return forbidden && !allowed;
}

function checkRule(rule, extensions, extractImports) {
  const root = path.join(repository, rule.root);
  const files = walk(root, extensions).sort();
  if (files.length === 0) {
    failures.push(`${rule.name}: no source files found under ${rule.root}`);
  }
  for (const file of files) {
    const repoFile = relative(file);
    if (excluded(repoFile, rule)) continue;
    filesChecked += 1;
    const source = fs.readFileSync(file, "utf8");
    for (const importPath of extractImports(source)) {
      importsChecked += 1;
      if (violates(importPath, rule)) {
        failures.push(`${repoFile}: ${rule.name} forbids import ${importPath}`);
      }
    }
  }
}

if (contract.version !== 1 || !contract.owner) {
  failures.push("dependency boundary contract must declare version 1 and an owner");
}

for (const rule of contract.go || []) {
  checkRule(rule, [".go"], goImports);
}
for (const rule of contract.frontend || []) {
  checkRule(rule, [".ts", ".tsx"], frontendImports);
}

if (failures.length > 0) {
  console.error(`dependency boundary check failed (${failures.length} finding(s)):`);
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log(
  `dependency boundaries passed: ${contract.go.length + contract.frontend.length} rules, ${filesChecked} files, ${importsChecked} imports`,
);
