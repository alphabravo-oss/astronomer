#!/usr/bin/env node
// One-time, deterministic decomposition of the legacy API facade. Kept as a
// guard/documentation artifact: it refuses to run after extraction.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const target = path.join(repoRoot, 'frontend/src/lib/api.ts');
const source = fs.readFileSync(target, 'utf8');
const marker = '// ============================================================\n// API Client Functions\n// ============================================================\n';
const markerAt = source.indexOf(marker);
if (markerAt < 0) throw new Error('Cannot find API client marker');
if (source.includes("from '@/lib/api/transport'")) {
  console.log('Frontend API transport is already extracted.');
  process.exit(0);
}
const retainedImports = [
  "import type { AxiosError } from 'axios';",
  "import type { APIResponse, PaginatedResponse } from '@/types';",
  "import type { OpenAPIComponents } from '@/types/openapi.generated';",
  "import { API_BASE, wsBase } from '@/lib/env';",
  "import api from '@/lib/api/transport';",
  '',
  'export default api;',
  '',
  'type RequireKeys<T, K extends keyof T> = Omit<T, K> & { [P in K]-?: Exclude<T[P], undefined> };',
  'const API_BASE_URL = API_BASE;',
  '',
].join('\n');
fs.writeFileSync(target, retainedImports + source.slice(markerAt));
console.log('Extracted the shared HTTP transport from frontend/src/lib/api.ts.');
