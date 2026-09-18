#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { gzipSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const maxRawBytes = 650_000;
const maxGzipBytes = 220_000;

// Follow emitted static dependencies, including those introduced by manualChunks.
// Dynamic imports outside the explicitly selected route remain lazy.
export function eagerAssets(manifest, entries) {
  const visited = new Set();
  const files = new Set();
  function visit(key) {
    if (visited.has(key)) return;
    const chunk = manifest[key];
    if (!chunk) throw new Error(`Missing bundle manifest entry: ${key}`);
    visited.add(key);
    files.add(chunk.file);
    for (const css of chunk.css ?? []) files.add(css);
    for (const dependency of chunk.imports ?? []) visit(dependency);
  }
  for (const entry of entries) visit(entry);
  return [...files].sort();
}

export function measureEagerClosure(manifest, entries, readAsset) {
  const files = eagerAssets(manifest, entries);
  let raw = 0;
  let gzip = 0;
  // Sum separately compressed files, matching individual HTTP transfers.
  for (const file of files) {
    const bytes = readAsset(file);
    raw += bytes.length;
    gzip += gzipSync(bytes).length;
  }
  return { files, raw, gzip };
}

function main() {
  const distRoot = path.join(frontendRoot, 'dist');
  const assetsRoot = path.join(distRoot, 'assets');
  const manifestPath = path.join(distRoot, '.vite', 'manifest.json');
  if (!fs.existsSync(manifestPath)) {
    throw new Error('Bundle budget requires frontend/dist/.vite/manifest.json; run `npm run build` first.');
  }
  const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
  const budgets = JSON.parse(fs.readFileSync(path.join(frontendRoot, 'bundle-budget.json'), 'utf8'));
  const chunks = fs.readdirSync(assetsRoot)
    .filter((name) => name.endsWith('.js'))
    .map((name) => {
      const bytes = fs.readFileSync(path.join(assetsRoot, name));
      return { name, raw: bytes.length, gzip: gzipSync(bytes).length };
    });
  if (chunks.length === 0) throw new Error('Bundle budget found no JavaScript chunks.');

  const failures = [];
  for (const chunk of chunks) {
    if (chunk.raw > maxRawBytes || chunk.gzip > maxGzipBytes) {
      failures.push(`${chunk.name}: ${chunk.raw} raw / ${chunk.gzip} gzip bytes exceeds per-chunk ${maxRawBytes} / ${maxGzipBytes}`);
    }
  }
  for (const [name, budget] of Object.entries(budgets.closures)) {
    const result = measureEagerClosure(manifest, budget.entries, (file) => fs.readFileSync(path.join(distRoot, file)));
    console.log(`${name}: ${result.files.length} eager JS/CSS assets, ${result.gzip} gzip bytes (ceiling ${budget.maxGzipBytes})`);
    if (result.gzip > budget.maxGzipBytes) {
      failures.push(`${name} eager closure exceeds its gzip ceiling by ${result.gzip - budget.maxGzipBytes} bytes`);
    }
  }
  if (failures.length) {
    console.error(`Frontend bundle budget failed:\n${failures.map((failure) => `  ${failure}`).join('\n')}`);
    process.exitCode = 1;
  } else {
    console.log(`Frontend bundle budget passed for ${chunks.length} chunks and ${Object.keys(budgets.closures).length} eager closures.`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(); } catch (error) {
    console.error(error.message);
    process.exitCode = 2;
  }
}
