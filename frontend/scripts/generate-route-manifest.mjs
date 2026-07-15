#!/usr/bin/env node
/**
 * P7.1 — Route-smoke manifest generator.
 *
 * Scans `src/routes/**` with the SAME file conventions as
 * @tanstack/router-plugin (see vite.config.ts): `*.test.(ts|tsx)` ignored,
 * `-`-prefixed files are co-located non-route modules, `route.tsx` files are
 * layouts (never leaf URLs), `index.tsx` is a leaf page and `$.tsx` is a
 * splat leaf. Emits one URL per leaf route with `$param` segments substituted
 * from PARAM_FIXTURES and splats substituted from SPLAT_FIXTURES.
 *
 * Two loud-failure guards (R7, gate anti-vacuousness):
 *  - any `$param` or splat without a fixture is a hard error, and
 *  - the manifest length must equal EXPECTED_ROUTE_COUNT, so silently
 *    dropped (or accidentally added) routes fail the smoke tier instead of
 *    shrinking it.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, '..');
const routesDir = path.join(frontendRoot, 'src/routes');
const outputPath = path.join(frontendRoot, 'tests/e2e-smoke/route-manifest.generated.json');

// Appendix A counts 107 pages with the `[[...slug]]` optional catch-all
// (custom-resources) as ONE page; its TanStack port is an `index.tsx` +
// `$.tsx` PAIR, and the manifest crawls both the empty and the populated
// splat variant — hence 108 URLs for 107 inventory pages.
const EXPECTED_ROUTE_COUNT = 108;

// One fixture per `$param` name, shared across every route that uses it.
// The route-smoke stubs answer any /api/v1 GET, so the values only need to
// be structurally valid for the page code (e.g. `resource` must be a key of
// k8s-paths RESOURCE_DEFS or the drilldown renders its empty state).
const PARAM_FIXTURES = {
  id: 'c-smoke-1',
  nodeName: 'node-smoke-1',
  resource: 'deployments',
  instanceId: 'argo-smoke-1',
  appId: 'app-smoke-1',
  restoreId: 'restore-smoke-1',
  runId: 'run-smoke-1',
  credId: 'cred-smoke-1',
  kind: 'deployment',
  namespace: 'default',
  name: 'smoke-app',
  scanId: 'scan-smoke-1',
  key: 'smoke-template',
};

// Splat (`$.tsx`) fixtures, keyed by the route directory relative to
// src/routes. These are the POPULATED variants; the empty variant of the old
// `[[...slug]]` optional catch-all is the sibling `index.tsx` route.
const SPLAT_FIXTURES = {
  // `[...path]` drilldown: [namespace, name] for a namespaced resource.
  'dashboard/clusters/$id/$resource': 'default/smoke-app',
  // `[[...slug]]` populated variant: [group, version, plural].
  'dashboard/clusters/$id/custom-resources': 'cert-manager.io/v1/certificates',
};

// Deep-link search params a page needs to render its main content (e.g. the
// reset form only renders with a token present).
const SEARCH_FIXTURES = {
  '/auth/login/reset-password': '?token=smoke-reset-token',
};

/** Recursively collect leaf route files (index.tsx / $.tsx). */
function collectRouteFiles(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith('-') || entry.name === '__tests__') continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...collectRouteFiles(full));
      continue;
    }
    if (/\.test\.(ts|tsx)$/.test(entry.name)) continue; // routeFileIgnorePattern
    if (entry.name === '__root.tsx' || entry.name === 'route.tsx') continue; // layouts
    if (entry.name === 'index.tsx' || entry.name === '$.tsx') out.push(full);
  }
  return out;
}

function substituteSegment(segment, file) {
  if (!segment.startsWith('$')) return segment;
  const param = segment.slice(1);
  const fixture = PARAM_FIXTURES[param];
  if (fixture === undefined) {
    console.error(`route-manifest: no PARAM_FIXTURES entry for "$${param}" (needed by ${file})`);
    process.exit(1);
  }
  return fixture;
}

const files = collectRouteFiles(routesDir).sort();
const manifest = files.map((file) => {
  const rel = path.relative(routesDir, file).split(path.sep);
  const base = rel.pop(); // index.tsx | $.tsx
  const routeDir = rel.join('/');
  const routeId = '/' + [...rel, base].join('/');
  let url = '/' + rel.map((seg) => substituteSegment(seg, file)).join('/');
  if (base === '$.tsx') {
    const splat = SPLAT_FIXTURES[routeDir];
    if (splat === undefined) {
      console.error(`route-manifest: no SPLAT_FIXTURES entry for "${routeDir}" (needed by ${file})`);
      process.exit(1);
    }
    url = `${url}/${splat}`;
  }
  if (url === '/') url = '/'; // root index
  const search = SEARCH_FIXTURES['/' + rel.join('/')] ?? '';
  return {
    routeId,
    url: url + search,
    // Auth pages run without the seeded session and assert their form
    // renders; everything else is crawled behind seedAuth.
    kind: routeId.startsWith('/auth/') ? 'auth' : 'app',
  };
});

if (manifest.length !== EXPECTED_ROUTE_COUNT) {
  console.error(
    `route-manifest: expected ${EXPECTED_ROUTE_COUNT} routes but generated ${manifest.length}. ` +
      'A route was added or dropped: update EXPECTED_ROUTE_COUNT deliberately (and Appendix A) ' +
      'instead of letting the smoke tier shrink silently.',
  );
  process.exit(1);
}

const dupes = manifest.map((m) => m.url).filter((u, i, all) => all.indexOf(u) !== i);
if (dupes.length > 0) {
  console.error(`route-manifest: duplicate URLs generated: ${[...new Set(dupes)].join(', ')}`);
  process.exit(1);
}

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, JSON.stringify(manifest, null, 2) + '\n');
console.log(`route-manifest: ${manifest.length} routes -> ${path.relative(frontendRoot, outputPath)}`);
