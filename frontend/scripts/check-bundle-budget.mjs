#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { gzipSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const assetsRoot = path.join(frontendRoot, 'dist', 'assets');
const maxRawBytes = 650_000;
const maxGzipBytes = 220_000;

if (!fs.existsSync(assetsRoot)) {
  console.error('Bundle budget gate requires frontend/dist; run `npm run build` first.');
  process.exit(2);
}

const chunks = fs.readdirSync(assetsRoot)
  .filter((name) => name.endsWith('.js'))
  .map((name) => {
    const bytes = fs.readFileSync(path.join(assetsRoot, name));
    return { name, raw: bytes.length, gzip: gzipSync(bytes).length };
  })
  .sort((a, b) => b.raw - a.raw);

if (chunks.length === 0) {
  console.error('Bundle budget gate found no JavaScript chunks in frontend/dist/assets.');
  process.exit(2);
}

const failures = chunks.filter((chunk) => chunk.raw > maxRawBytes || chunk.gzip > maxGzipBytes);
if (failures.length > 0) {
  console.error(`Frontend bundle budget failed (${maxRawBytes} raw / ${maxGzipBytes} gzip bytes per chunk):`);
  for (const chunk of failures) {
    console.error(`  ${chunk.name}: ${chunk.raw} raw, ${chunk.gzip} gzip bytes`);
  }
  process.exit(1);
}

const largestRaw = chunks[0];
const largestGzip = chunks.slice().sort((a, b) => b.gzip - a.gzip)[0];
console.log(
  `Frontend bundle budget passed for ${chunks.length} chunk(s); ` +
  `largest raw ${largestRaw.name}=${largestRaw.raw}, ` +
  `largest gzip ${largestGzip.name}=${largestGzip.gzip} bytes.`,
);
