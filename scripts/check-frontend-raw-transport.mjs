#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const apiRoot = path.join(repoRoot, 'frontend/src/lib/api');

// Compatibility budget: ordinary JSON REST calls that still bypass the
// generated OpenAPI operation client. Counts may decrease, never increase;
// a new file with raw calls fails until it is generated-client based.
const LEGACY_JSON_BUDGET = new Map([
  ['admin-operations.ts', 0],
  ['admin-security.ts', 0],
  ['cluster-snapshots.ts', 0],
  ['metrics.ts', 0],
  ['native-rbac.ts', 0],
  ['resource-search.ts', 0],
  ['settings-compliance-export.ts', 0],
  ['settings-email.ts', 0],
]);

// Dedicated opaque Kubernetes adapters cannot be represented by fixed-path
// generated operations. They remain isolated and have their own no-growth cap.
const INTENTIONAL_API_ADAPTER_BUDGET = new Map([
  ['kubernetes-proxy.ts', 2],
  ['kubernetes-resources.ts', 23],
]);

// Non-JSON transports are intentionally outside the JSON-operation budget.
// The marker check keeps this exception inventory honest if an adapter moves.
const INTENTIONAL_NON_JSON_ADAPTERS = [
  ['frontend/src/lib/api/k8s-watch.ts', 'EventSource', 'Kubernetes watch stream'],
  ['frontend/src/lib/live/stream.ts', 'EventSource', 'authenticated live-event stream'],
  ['frontend/src/lib/api/charlie.ts', 'EventSource', 'Charlie session stream'],
  ['frontend/src/lib/api/workloads.ts', 'WebSocket', 'pod watch stream'],
  ['frontend/src/components/clusters/cluster-shell.tsx', 'WebSocket', 'cluster shell'],
  ['frontend/src/components/workloads/pod-terminal.tsx', 'WebSocket', 'pod terminal'],
  ['frontend/src/lib/api/settings-compliance-export.ts', 'fetch(downloadUrl)', 'pre-signed compliance export download'],
];

const rawCall = /\bapi\s*\.\s*(?:get|post|put|patch|delete|request)\b/g;
const actual = new Map();
for (const entry of fs.readdirSync(apiRoot, { withFileTypes: true })) {
  if (!entry.isFile() || !entry.name.endsWith('.ts') || entry.name.endsWith('.test.ts')) continue;
  const source = fs.readFileSync(path.join(apiRoot, entry.name), 'utf8');
  const count = [...source.matchAll(rawCall)].length;
  if (count > 0) actual.set(entry.name, count);
}

const failures = [];
for (const [file, count] of actual) {
  const budget = LEGACY_JSON_BUDGET.get(file) ?? INTENTIONAL_API_ADAPTER_BUDGET.get(file);
  if (budget === undefined) failures.push(`${file}: ${count} unclassified raw transport call(s)`);
  else if (count > budget) failures.push(`${file}: raw transport count grew ${budget} -> ${count}`);
}
for (const [file, budget] of [...LEGACY_JSON_BUDGET, ...INTENTIONAL_API_ADAPTER_BUDGET]) {
  const count = actual.get(file) ?? 0;
  if (count < budget) failures.push(`${file}: raw transport count fell ${budget} -> ${count}; lower the ratchet baseline`);
}

for (const [file, marker, reason] of INTENTIONAL_NON_JSON_ADAPTERS) {
  const absolute = path.join(repoRoot, file);
  if (!fs.existsSync(absolute) || !fs.readFileSync(absolute, 'utf8').includes(marker)) {
    failures.push(`${file}: stale ${reason} exception (missing marker ${JSON.stringify(marker)})`);
  }
}

const compatibilityCount = [...actual]
  .filter(([file]) => LEGACY_JSON_BUDGET.has(file))
  .reduce((sum, [, count]) => sum + count, 0);
const intentionalCount = [...actual]
  .filter(([file]) => INTENTIONAL_API_ADAPTER_BUDGET.has(file))
  .reduce((sum, [, count]) => sum + count, 0);

console.log(`Frontend raw transport inventory: ${compatibilityCount} compatibility, ${intentionalCount} intentional Kubernetes adapter call(s).`);
if (failures.length > 0) {
  console.error('Frontend raw transport gate failed:');
  for (const failure of failures) console.error(`- ${failure}`);
  process.exitCode = 1;
}
