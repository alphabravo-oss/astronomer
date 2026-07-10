#!/usr/bin/env node
// OpenAPI coverage tool.
//
// Compares the real chi route table (docs/routes.json, produced by
// `DUMP_ROUTES=1 go test ./internal/server/ -run TestRouteDumpCanBeGenerated`)
// against the hand-curated paths in docs/openapi.yaml.
//
// Reports:
//   - documented   : route operations present in both the router and the spec
//   - missing       : router operations NOT documented in openapi.yaml
//   - extra         : openapi.yaml operations with no matching router route
//   - coverage      : documented / total-router-operations
//
// Modes:
//   (default)  print the report, always exit 0
//   --check    exit non-zero when there is drift (any extra spec operation, i.e.
//              a documented operation that no longer maps to a real route).
//
// Coverage is intentionally informational: the spec documents only the stable
// public surface, so "missing" is expected to be large and does NOT fail --check.
// Drift that DOES fail --check is an `extra` operation: the spec describes a
// path/method the router no longer serves AND that is not a known
// nil-gated route (see KNOWN_NIL_GATED below).
//
// CAVEAT: docs/routes.json is walked from the route-security test router,
// which leaves some handler dependencies nil. chi omits routes whose handler
// pointer is nil, so a handful of real, documented routes are absent from the
// dump through no fault of the spec. Those are listed in KNOWN_NIL_GATED so
// --check does not raise false drift on them. Anything NOT on that list that is
// documented-but-unrouted IS treated as genuine drift and fails --check.

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const requireFromFrontend = createRequire(path.join(repoRoot, 'frontend/package.json'));
const yaml = requireFromFrontend('js-yaml');

const args = new Set(process.argv.slice(2));
const check = args.has('--check');

const routesPath = path.join(repoRoot, 'docs/routes.json');
const specPath = path.join(repoRoot, 'docs/openapi.yaml');

if (!fs.existsSync(routesPath)) {
  console.error(`Missing ${path.relative(repoRoot, routesPath)}.`);
  console.error('Run: DUMP_ROUTES=1 go test ./internal/server/ -run TestRouteDumpCanBeGenerated');
  process.exit(2);
}

const HTTP_METHODS = new Set(['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace']);

// Documented operations whose handler dependency is nil in the route-dump test
// router, so chi omits them from docs/routes.json. They are real, served routes
// in production; --check must not treat them as spec drift. Keys are
// "METHOD <normalized-path>" (params collapsed to {}, no trailing slash).
const KNOWN_NIL_GATED = new Set([
  'GET /api/v1/admin/backup-drill',
  'GET /api/v1/alerting/channels',
  'POST /api/v1/alerting/channels',
  'GET /api/v1/alerting/events',
  // P-03 alert inhibitions — the Alerting handler is nil in the route-dump
  // router, so chi omits these real, served routes from docs/routes.json.
  'GET /api/v1/admin/alerting/inhibitions',
  'POST /api/v1/admin/alerting/inhibitions',
  'GET /api/v1/admin/alerting/inhibitions/{}',
  'PUT /api/v1/admin/alerting/inhibitions/{}',
  'DELETE /api/v1/admin/alerting/inhibitions/{}',
  // P-04 custom Gatekeeper constraint authoring — the Gatekeeper handler is
  // nil in the route-dump router, so chi omits these real, served routes.
  'GET /api/v1/clusters/{}/gatekeeper/constraints',
  'POST /api/v1/clusters/{}/gatekeeper/constraints',
  'POST /api/v1/clusters/{}/gatekeeper/constraints/validate',
  'DELETE /api/v1/clusters/{}/gatekeeper/constraints/{}',
  'GET /api/v1/argocd/instances',
  'GET /api/v1/argocd/instances/{}/orphan-report',
  'GET /api/v1/argocd/operations',
  'GET /api/v1/argocd/operations/{}',
  'POST /api/v1/argocd/applications/{}/sync',
  'POST /api/v1/argocd/instances/{}/applications/{}/sync',
  'GET /api/v1/clusters/{}/vulnerabilities/images',
  'GET /api/v1/clusters/{}/vulnerabilities/summary',
  'GET /api/v1/extensions',
  'POST /api/v1/extensions',
  'POST /api/v1/extensions/{}/disable',
  'POST /api/v1/extensions/{}/enable',
  'GET /api/v1/extensions/sample-manifest',
  'POST /api/v1/extensions/validate',
  'POST /api/v1/extensions/verify-bundle',
  // Extension runtime (Tier 1/2). Handler is nil in the route-dump router,
  // so chi omits these real, served routes from the dump — same as SCIM.
  'GET /api/v1/extensions/mounts',
  'POST /api/v1/extensions/{}/data/{}',
  'POST /api/v1/extensions/{}/token',
  'GET /api/v1/settings/features',
  'GET /api/v1/tools',
  // SCIM 2.0 provisioning (migration 114). The SCIM handler is nil in
  // the route-dump test router, so chi omits these routes from
  // docs/routes.json even though they are real, served routes in prod.
  'POST /scim/v2/Users',
  'GET /scim/v2/Users',
  'GET /scim/v2/Users/{}',
  'PUT /scim/v2/Users/{}',
  'PATCH /scim/v2/Users/{}',
  'DELETE /scim/v2/Users/{}',
  'GET /scim/v2/Groups',
  'GET /scim/v2/Groups/{}',
  'GET /scim/v2/ServiceProviderConfig',
  'GET /scim/v2/ResourceTypes',
  'GET /scim/v2/Schemas',
]);

// Normalize a path so router and spec forms compare equal:
//   - strip a single trailing slash (except the bare root)
//   - replace every {param} placeholder with a single {} token, since the
//     router and the spec use different parameter names for the same slot
//     (router {id} vs spec {name}/{cluster_id}).
function normalizePath(p) {
  // Detect a trailing catch-all segment BEFORE collapsing param names, so we
  // can tell a real catch-all ('*' on the router, '{path}'/'{...path}' on the
  // spec) apart from an ordinary trailing single-segment id (e.g. '{id}').
  //   - router side: a literal trailing '/*'
  //   - spec side:   a trailing path template whose PARAMETER NAME is 'path'
  //                  (or ends in 'path'), e.g. '/{path}' or '/{...path}'.
  // Both model chi's '*' wildcard (they consume the remainder of the URL), so
  // they are folded to the same '/*' sentinel. Non-terminal '{}' params and
  // ordinary trailing ids (e.g. '/{session_id}') are left as real '{}' slots.
  const trailingCatchAll = /\/(\*|\{\.{0,3}[^}]*path\})$/.test(p);
  let out = p.replace(/\{[^}]*\}/g, '{}');
  if (out.length > 1) out = out.replace(/\/+$/, '');
  if (trailingCatchAll) out = out.replace(/\/\{\}$/, '/*');
  return out;
}

function opKey(method, p) {
  return `${method.toUpperCase()} ${normalizePath(p)}`;
}

// Router operations.
const routes = JSON.parse(fs.readFileSync(routesPath, 'utf8'));
const routerOps = new Map(); // key -> { method, pattern }
for (const r of routes) {
  // OpenAPI 3 has no `connect` operation object, so the spec can never emit a
  // CONNECT operation. chi registers CONNECT on every HandleFunc/catch-all
  // route, but those are not documentable; counting them would permanently
  // inflate `missing` and depress coverage. Exclude CONNECT from both the
  // numerator and the denominator.
  if (r.method.toUpperCase() === 'CONNECT') continue;
  routerOps.set(opKey(r.method, r.pattern), { method: r.method.toUpperCase(), pattern: r.pattern });
}

// Spec operations.
const spec = yaml.load(fs.readFileSync(specPath, 'utf8'));
const paths = spec?.paths ?? {};
const specOps = new Map(); // key -> { method, pattern }
for (const [p, item] of Object.entries(paths)) {
  if (!item || typeof item !== 'object') continue;
  for (const method of Object.keys(item)) {
    if (!HTTP_METHODS.has(method.toLowerCase())) continue;
    specOps.set(opKey(method, p), { method: method.toUpperCase(), pattern: p });
  }
}

const documented = [];
const missing = [];
for (const [key, op] of routerOps) {
  if (specOps.has(key)) documented.push(op);
  else missing.push(op);
}

const extra = [];      // documented but unrouted AND not known-nil-gated = drift
const nilGated = [];   // documented but unrouted because handler is nil in the dump router
for (const [key, op] of specOps) {
  if (routerOps.has(key)) continue;
  if (KNOWN_NIL_GATED.has(key)) nilGated.push(op);
  else extra.push(op);
}

const sortOps = (ops) => ops.sort((a, b) =>
  a.pattern === b.pattern ? a.method.localeCompare(b.method) : a.pattern.localeCompare(b.pattern));
sortOps(documented);
sortOps(missing);
sortOps(extra);
sortOps(nilGated);

const totalRouter = routerOps.size;
const coverage = totalRouter === 0 ? 0 : (documented.length / totalRouter) * 100;

console.log('OpenAPI coverage report');
console.log('=======================');
console.log(`router operations   : ${totalRouter}`);
console.log(`spec operations     : ${specOps.size}`);
console.log(`documented (matched): ${documented.length}`);
console.log(`missing (undocumented routes): ${missing.length}`);
console.log(`extra (spec drift, no route): ${extra.length}`);
console.log(`nil-gated (unrouted in dump, allowlisted): ${nilGated.length}`);
console.log(`coverage            : ${coverage.toFixed(1)}%  (${documented.length}/${totalRouter})`);

if (extra.length > 0) {
  console.log('\nDRIFT — documented in openapi.yaml but no matching route (and not allowlisted):');
  for (const op of extra) console.log(`  ${op.method} ${op.pattern}`);
}

if (nilGated.length > 0 && args.has('--verbose')) {
  console.log('\nNIL-GATED — documented routes absent from the dump (nil handler in test router):');
  for (const op of nilGated) console.log(`  ${op.method} ${op.pattern}`);
}

if (args.has('--verbose')) {
  console.log('\nMISSING — served by router but not in openapi.yaml:');
  for (const op of missing) console.log(`  ${op.method} ${op.pattern}`);
}

if (check && extra.length > 0) {
  console.error(`\nFAIL: ${extra.length} documented operation(s) no longer map to a route (spec drift).`);
  console.error('Fix the spec, or if intentionally nil-gated, add to KNOWN_NIL_GATED in this script.');
  process.exit(1);
}

process.exit(0);
