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
//   --check    exit non-zero when there is drift in either direction.
//
// Every mounted operation must be explicitly documented and classified. A
// missing operation is contract drift just like a stale operation is.
//
// CAVEAT: docs/routes.json is walked from the route-security test router,
// which may leave a handler dependency nil. chi omits routes whose handler
// pointer is nil, so such a route is absent from the dump through no fault of
// the spec. Those are listed in KNOWN_NIL_GATED so --check does not raise false
// drift on them. Anything NOT on that list that is documented-but-unrouted IS
// treated as genuine drift and fails --check. Most of that list is now inert:
// the test router wires nearly every handler dep (see
// TestRouteSecurityRouterWiresEveryHandlerDependency), which is also why the
// dump — and therefore the "missing" count — grew to the real route surface.

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
  // These two operator-enabled handlers require real infrastructure and stay
  // deliberately absent from the route-shape fixture. Every other normal
  // route owner is constructed, so this list cannot mask composition drift.
  'GET /api/v1/admin/notification-templates',
  'GET /api/v1/admin/notification-templates/{}',
  'PUT /api/v1/admin/notification-templates/{}',
  'DELETE /api/v1/admin/notification-templates/{}',
  'POST /api/v1/admin/notification-templates/{}/preview',
  'GET /api/v1/admin/notification-templates/{}/variables',
  'GET /api/v1/clusters/{}/control-plane-snapshots',
  'POST /api/v1/clusters/{}/control-plane-snapshots',
  'GET /api/v1/clusters/{}/control-plane-snapshots/{}',
  'GET /api/v1/clusters/{}/control-plane-snapshots/{}/restore-guidance',
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
  // OpenAPI 3 has no operation objects for CONNECT or the HTTP QUERY method,
  // so the spec can never emit either. chi registers both on catch-all proxy
  // routes; they remain visible in the route/security inventories, but cannot
  // participate in OpenAPI coverage without creating permanent false drift.
  if (['CONNECT', 'QUERY'].includes(r.method.toUpperCase())) continue;
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

if (check && (extra.length > 0 || missing.length > 0)) {
  console.error(`\nFAIL: OpenAPI drift: ${missing.length} mounted operation(s) missing and ${extra.length} stale operation(s).`);
  console.error('Run the route dump and scripts/sync-openapi-routes.mjs, or explicitly classify an intentionally nil-gated route.');
  process.exit(1);
}

process.exit(0);
