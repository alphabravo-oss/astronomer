#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const args = new Set(process.argv.slice(2));
const skipDirs = new Set([
  '.git',
  '.next',
  'coverage',
  'dist',
  'node_modules',
  'out',
  'tmp',
  'vendor',
]);

function rel(file) {
  return path.relative(repoRoot, file).replaceAll(path.sep, '/');
}

function read(file) {
  return fs.readFileSync(file, 'utf8');
}

function exists(file) {
  return fs.existsSync(file);
}

function walk(dir, include) {
  if (!exists(dir)) return [];
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (skipDirs.has(entry.name)) continue;
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walk(file, include));
      continue;
    }
    if (!entry.isFile()) continue;
    if (!include || include(file)) out.push(file);
  }
  return out.sort();
}

function lineFindings(files, matcher) {
  const findings = [];
  for (const file of files) {
    const lines = read(file).split(/\r?\n/);
    lines.forEach((line, index) => {
      const detail = matcher(line, file);
      if (!detail) return;
      findings.push({
        file: rel(file),
        line: index + 1,
        detail: typeof detail === 'string' ? detail : line.trim(),
      });
    });
  }
  return findings;
}

function packageDependencyDuplicates() {
  const pkgFile = path.join(repoRoot, 'frontend/package.json');
  const pkg = JSON.parse(read(pkgFile));
  const sections = [
    'dependencies',
    'devDependencies',
    'optionalDependencies',
    'peerDependencies',
  ];
  const seen = new Map();
  for (const section of sections) {
    for (const name of Object.keys(pkg[section] ?? {})) {
      const rows = seen.get(name) ?? [];
      rows.push({ section, version: pkg[section][name] });
      seen.set(name, rows);
    }
  }
  return [...seen.entries()]
    .filter(([, rows]) => rows.length > 1)
    .map(([name, rows]) => ({ name, rows }));
}

function frontendFiles() {
  return walk(path.join(repoRoot, 'frontend/src'), (file) =>
    ['.ts', '.tsx'].includes(path.extname(file)),
  );
}

function directHttpCallSites(files) {
  return lineFindings(files, (line, file) => {
    const r = rel(file);
    const allowed =
      r === 'frontend/src/lib/api.ts' ||
      r.startsWith('frontend/src/lib/api/');
    if (allowed) return null;
    if (/\bfetch\s*\(/.test(line)) return 'direct fetch outside frontend API layer';
    if (/\baxios\s*\./.test(line)) return 'direct axios call outside frontend API layer';
    return null;
  });
}

function directToastCallSites(files) {
  return lineFindings(files, (line, file) => {
    const r = rel(file);
    if (r === 'frontend/src/lib/toast.ts') return null;
    if (
      r === 'frontend/src/app/(components)/providers.tsx' &&
      /import\s*\{\s*Toaster\s*\}\s*from\s*['"]sonner['"]/.test(line)
    ) {
      return null;
    }
    if (/from\s*['"]sonner['"]/.test(line) || /import\s*\(\s*['"]sonner['"]\s*\)/.test(line)) {
      return 'direct sonner import outside toast wrapper/provider';
    }
    if (/\btoast\.(error|success|warning|info|message|promise)\s*\(/.test(line)) {
      return 'direct toast call outside shared toast wrapper';
    }
    return null;
  });
}

function localResponseShapes(files) {
  return lineFindings(
    files.filter((file) => rel(file).startsWith('frontend/src/app/')),
    (line) =>
      /\b(type|interface)\s+ResponseShape\b/.test(line)
        ? 'local ResponseShape type should move into a feature API module'
        : null,
  );
}

function stripImportsPreservingLines(source) {
  return source.replace(/^import[\s\S]*?from\s+['"][^'"]+['"];?$/gm, (match) =>
    '\n'.repeat(match.split(/\r?\n/).length - 1),
  );
}

function duplicateFrontendApiShapeTypes(files) {
  const groups = new Map();
  for (const file of files) {
    if (rel(file) === 'frontend/src/types/openapi.generated.ts') continue;
    const source = stripImportsPreservingLines(read(file));
    for (const match of source.matchAll(
      /^\s*(?:export\s+)?(?:interface|type)\s+([A-Za-z][A-Za-z0-9_]*(?:Request|Response|WriteRequest))\b/gm,
    )) {
      const rows = groups.get(match[1]) ?? [];
      rows.push({
        file: rel(file),
        line: source.slice(0, match.index).split(/\r?\n/).length,
      });
      groups.set(match[1], rows);
    }
  }
  return [...groups.entries()]
    .filter(([, rows]) => rows.length > 1)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, rows]) => ({ name, rows }));
}

function localStatusBadgeComponents(files) {
  return lineFindings(files, (line, file) => {
    if (rel(file) === 'frontend/src/components/ui/status-badge.tsx') return null;
    if (/\b(function|const|interface)\s+StatusBadge\b/.test(line)) {
      return 'local generic StatusBadge should use the shared UI component or a domain-specific name';
    }
    return null;
  });
}

function rawTableTagCallSites(files) {
  return lineFindings(files, (line, file) => {
    if (rel(file) === 'frontend/src/components/ui/table.tsx') return null;
    if (/<\/?(table|thead|tbody|tr|th|td)\b/.test(line)) {
      return 'raw table tag should use components/ui/table primitives';
    }
    return null;
  });
}

function rawOverlayCallSites(files) {
  const allowed = new Set([
    'frontend/src/components/ui/overlay-shell.tsx',
    'frontend/src/components/ui/modal-shell.tsx',
    'frontend/src/components/ui/drawer-shell.tsx',
  ]);
  return lineFindings(files, (line, file) => {
    if (allowed.has(rel(file))) return null;
    if (/\bfixed\s+inset-0\b/.test(line)) {
      return 'raw overlay root/backdrop should use components/ui/overlay-shell primitives';
    }
    if (/\bbg-black\/(?:30|40|50|60)\b/.test(line)) {
      return 'raw modal backdrop color should use components/ui/overlay-shell primitives';
    }
    return null;
  });
}

function authSessionLiteralCallSites(files) {
  const allowed = new Set([
    'frontend/src/lib/auth/session.ts',
    'frontend/src/lib/__tests__/auth-session.test.ts',
  ]);
  return lineFindings(files, (line, file) => {
    if (allowed.has(rel(file))) return null;
    if (/'astronomer_(session|token|refresh)'/.test(line)) {
      return 'auth session cookie/token key literal should come from lib/auth/session';
    }
    return null;
  });
}

function pageQueryKeys(files) {
  return lineFindings(
    files.filter((file) => rel(file).startsWith('frontend/src/app/')),
    (line) => {
      if (/\bqueryKey\s*:\s*\[/.test(line)) {
        return 'inline React Query key in page component';
      }
      if (/\bconst\s+qk\s*=/.test(line)) {
        return 'page-local query key factory';
      }
      return null;
    },
  );
}

function goFiles() {
  return walk(path.join(repoRoot, 'internal'), (file) =>
    path.extname(file) === '.go' &&
    !rel(file).startsWith('internal/db/sqlc/') &&
    !rel(file).endsWith('_test.go'),
  );
}

function goReferenceFiles() {
  return walk(repoRoot, (file) =>
    path.extname(file) === '.go' &&
    !rel(file).startsWith('internal/db/sqlc/'),
  );
}

function duplicateUnexportedGoFunctions(files) {
  const groups = new Map();
  for (const file of files) {
    const lines = read(file).split(/\r?\n/);
    lines.forEach((line, index) => {
      const match = /^func\s+([a-z][A-Za-z0-9_]*)\s*\(/.exec(line);
      if (!match) return;
      if (match[1] === 'init') return;
      const rows = groups.get(match[1]) ?? [];
      rows.push({ file: rel(file), line: index + 1 });
      groups.set(match[1], rows);
    });
  }
  return [...groups.entries()]
    .filter(([, rows]) => rows.length > 1)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, rows]) => ({ name, rows }));
}

function sqlQueryDeclarations() {
  const queryFiles = walk(path.join(repoRoot, 'internal/db/queries'), (file) =>
    path.extname(file) === '.sql',
  );
  const declarations = [];
  for (const file of queryFiles) {
    const lines = read(file).split(/\r?\n/);
    lines.forEach((line, index) => {
      const match = /^--\s*name:\s+([A-Za-z][A-Za-z0-9_]*)\s+/.exec(line);
      if (!match) return;
      declarations.push({ name: match[1], file: rel(file), line: index + 1 });
    });
  }
  return declarations.sort((a, b) => a.name.localeCompare(b.name));
}

function sqlQueriesWithoutExternalUse(declarations, files) {
  const haystack = files.map((file) => read(file)).join('\n');
  return declarations.filter((decl) => {
    const re = new RegExp(`\\b${decl.name}\\b`);
    return !re.test(haystack);
  });
}

function componentKeyForFile(file) {
  return rel(file)
    .replace(/^frontend\/src\/components\//, '')
    .replace(/\.(tsx|ts)$/, '')
    .replace(/\/index$/, '');
}

function resolveImportToComponentKey(fromFile, specifier) {
  if (specifier.startsWith('@/components/')) {
    return specifier.replace(/^@\/components\//, '').replace(/\/index$/, '');
  }
  if (!specifier.startsWith('.')) return null;

  const base = path.resolve(path.dirname(fromFile), specifier);
  const candidates = [
    base,
    `${base}.tsx`,
    `${base}.ts`,
    path.join(base, 'index.tsx'),
    path.join(base, 'index.ts'),
  ];
  const componentRoot = path.join(repoRoot, 'frontend/src/components');
  const resolved = candidates.find((candidate) =>
    exists(candidate) &&
    candidate.startsWith(componentRoot) &&
    ['.ts', '.tsx'].includes(path.extname(candidate)),
  );
  return resolved ? componentKeyForFile(resolved) : null;
}

function componentImportCandidates(files) {
  const componentFiles = walk(path.join(repoRoot, 'frontend/src/components'), (file) =>
    ['.ts', '.tsx'].includes(path.extname(file)) &&
    !rel(file).includes('/__tests__/'),
  );
  const imported = new Set();
  for (const file of files) {
    const source = read(file);
    const specs = [
      ...source.matchAll(/from\s+['"]([^'"]+)['"]/g),
      ...source.matchAll(/import\s*\(\s*['"]([^'"]+)['"]\s*\)/g),
      ...source.matchAll(/import\s+['"]([^'"]+)['"]/g),
    ];
    for (const match of specs) {
      const componentKey = resolveImportToComponentKey(file, match[1]);
      if (componentKey) imported.add(componentKey);
    }
  }
  return componentFiles
    .map((file) => {
      const componentRel = componentKeyForFile(file);
      return { path: `@/components/${componentRel}`, file: rel(file) };
    })
    .filter((row) => !imported.has(row.path.replace(/^@\/components\//, '')))
    .sort((a, b) => a.path.localeCompare(b.path));
}

function helmTopLevelValueCandidates() {
  const valuesFile = path.join(repoRoot, 'deploy/chart/values.yaml');
  if (!exists(valuesFile)) return [];
  const keys = read(valuesFile)
    .split(/\r?\n/)
    .map((line) => /^([A-Za-z][A-Za-z0-9_-]*):\s*/.exec(line)?.[1])
    .filter(Boolean);
  const templateFiles = walk(path.join(repoRoot, 'deploy/chart/templates'), (file) =>
    ['.yaml', '.tpl'].includes(path.extname(file)),
  );
  const templates = templateFiles.map((file) => read(file)).join('\n');
  return [...new Set(keys)]
    .filter((key) => !new RegExp(`\\.Values\\.${key}\\b`).test(templates))
    .sort();
}

function countLines(files) {
  return files.reduce((sum, file) => sum + read(file).split(/\r?\n/).length, 0);
}

function bulletList(items, render, limit = 40) {
  if (items.length === 0) return '- None.\n';
  const visible = items.slice(0, limit).map((item) => `- ${render(item)}`).join('\n');
  const remaining = items.length - limit;
  return `${visible}${remaining > 0 ? `\n- ... ${remaining} more` : ''}\n`;
}

function location(item) {
  return `[\`${item.file}:${item.line}\`](${item.file}:${item.line})`;
}

function renderMarkdown(inventory) {
  const hardFailures =
    inventory.duplicateDependencies.length +
    inventory.directHttpCallSites.length +
    inventory.directToastCallSites.length +
    inventory.localResponseShapes.length +
    inventory.duplicateFrontendApiShapeTypes.length +
    inventory.localStatusBadgeComponents.length +
    inventory.rawTableTagCallSites.length +
    inventory.rawOverlayCallSites.length +
    inventory.authSessionLiteralCallSites.length +
    inventory.pageQueryKeys.length;
  const duplicateCandidateCount =
    inventory.localResponseShapes.length +
    inventory.duplicateFrontendApiShapeTypes.length +
    inventory.localStatusBadgeComponents.length +
    inventory.rawTableTagCallSites.length +
    inventory.rawOverlayCallSites.length +
    inventory.authSessionLiteralCallSites.length +
    inventory.pageQueryKeys.length +
    inventory.duplicateGoHelpers.length;
  const deadCodeCandidateCount =
    inventory.sqlQueriesWithoutExternalUse.length +
    inventory.componentImportCandidates.length +
    inventory.helmTopLevelValueCandidates.length;

  return `# Rancher-Quality Phase 0 Code Health Inventory

Status: Active
Generated by: \`node scripts/code-health-inventory.mjs --write\`
CI gate: \`npm run code-health\` from \`frontend/\`

This inventory supports Phase 0 duplicate/dead-code detection and Phase 10 cleanup. It intentionally separates hard failures from candidates that need owner review before removal.

## Scan Scope

- Frontend source files: ${inventory.frontendFileCount}
- Frontend source lines: ${inventory.frontendLineCount}
- Go source files under \`internal/\` excluding generated sqlc and tests: ${inventory.goFileCount}
- Go source files scanned for sqlc query references excluding generated sqlc: ${inventory.goReferenceFileCount}
- sqlc query declarations: ${inventory.sqlQueryCount}
- Component files scanned: ${inventory.componentFileCount}
- Helm top-level values scanned: ${inventory.helmTopLevelValueCount}

## Hard Gates

- Duplicate direct package declarations across frontend dependency sections: ${inventory.duplicateDependencies.length === 0 ? 'pass' : 'fail'}
- Direct \`fetch\` or \`axios\` calls outside \`frontend/src/lib/api*\`: ${inventory.directHttpCallSites.length === 0 ? 'pass' : 'fail'}
- Direct \`sonner\` imports or \`toast.*\` calls outside \`frontend/src/lib/toast.ts\`: ${inventory.directToastCallSites.length === 0 ? 'pass' : 'fail'}
- Page-local API \`ResponseShape\` types in app routes: ${inventory.localResponseShapes.length === 0 ? 'pass' : 'fail'}
- Duplicate frontend API shape type names ending in \`Request\`, \`WriteRequest\`, or \`Response\`: ${inventory.duplicateFrontendApiShapeTypes.length === 0 ? 'pass' : 'fail'}
- Local generic \`StatusBadge\` components outside the shared UI component: ${inventory.localStatusBadgeComponents.length === 0 ? 'pass' : 'fail'}
- Raw native table tags outside \`frontend/src/components/ui/table.tsx\`: ${inventory.rawTableTagCallSites.length === 0 ? 'pass' : 'fail'}
- Raw overlay roots/backdrops outside \`frontend/src/components/ui/overlay-shell.tsx\`: ${inventory.rawOverlayCallSites.length === 0 ? 'pass' : 'fail'}
- Auth session cookie/token key literals outside \`frontend/src/lib/auth/session.ts\`: ${inventory.authSessionLiteralCallSites.length === 0 ? 'pass' : 'fail'}
- Page-local or inline React Query keys in app routes: ${inventory.pageQueryKeys.length === 0 ? 'pass' : 'fail'}

The hard gates are enforced by \`scripts/code-health-inventory.mjs --check --verify-doc\`.

## Remove

These are hard-gate failures and should be removed or moved before merge.

### Duplicate Frontend Dependencies

${bulletList(inventory.duplicateDependencies, (item) =>
  `\`${item.name}\` appears in ${item.rows.map((row) => `\`${row.section}\` (${row.version})`).join(', ')}`,
)}
### Direct HTTP Calls Outside API Layer

${bulletList(inventory.directHttpCallSites, (item) =>
  `${location(item)} - ${item.detail}`,
)}
### Direct Toast Calls Outside Toast Wrapper

${bulletList(inventory.directToastCallSites, (item) =>
  `${location(item)} - ${item.detail}`,
)}
## Keep

- Test fakes and requesters with repeated names such as \`fakeRequester\` are intentionally package-local test scaffolding unless they leak into production packages.
- Specialized CloudCredential, Vault, and audit redactors are intentionally retained because their sentinels and stable JSON shapes are API/audit contracts; diagnostics and support bundles use \`internal/redaction\`.
- Query methods referenced only by narrow package interfaces should remain when the interface is the stable seam and package tests cover behavior.

## Needs Investigation

These are candidates, not removal approvals. Owners should classify each one as remove, keep, or consolidate before changing behavior.

### Duplicate-Code Candidates

Owner: frontend/API. Target abstraction: feature API modules under \`frontend/src/lib/api\`.

${bulletList(inventory.localResponseShapes, (item) =>
  `${location(item)} - ${item.detail}`,
)}
Owner: frontend/API. Target abstraction: one exported API shape per endpoint family.

${bulletList(inventory.duplicateFrontendApiShapeTypes, (item) =>
  `\`${item.name}\` in ${item.rows.map((row) => `[\`${row.file}:${row.line}\`](${row.file}:${row.line})`).join(', ')}`,
)}
Owner: frontend/platform. Target abstraction: shared \`components/ui/status-badge.tsx\` for generic statuses; domain-specific badges must use domain-specific names.

${bulletList(inventory.localStatusBadgeComponents, (item) =>
  `${location(item)} - ${item.detail}`,
)}
Owner: frontend/platform. Target abstraction: shared \`components/ui/table.tsx\` primitives for all table markup, with \`components/ui/data-table.tsx\` layered on top for searchable/paginated tables.

${bulletList(inventory.rawTableTagCallSites, (item) =>
  `${location(item)} - ${item.detail}`,
)}
Owner: frontend/platform. Target abstraction: shared \`components/ui/overlay-shell.tsx\`, \`modal-shell.tsx\`, or \`drawer-shell.tsx\` for all blocking overlays.

${bulletList(inventory.rawOverlayCallSites, (item) =>
  `${location(item)} - ${item.detail}`,
)}
Owner: frontend/platform. Target abstraction: shared \`frontend/src/lib/auth/session.ts\` constants and helpers.

${bulletList(inventory.authSessionLiteralCallSites, (item) =>
  `${location(item)} - ${item.detail}`,
)}
Owner: frontend/platform. Target abstraction: shared \`queryKeys\` or feature hook modules.

${bulletList(inventory.pageQueryKeys, (item) =>
  `${location(item)} - ${item.detail}`,
)}
Owner: backend/platform. Target abstraction: shared helper package only when call sites perform the same behavior.

${bulletList(inventory.duplicateGoHelpers, (item) =>
  `\`${item.name}\` in ${item.rows.map((row) => `[\`${row.file}:${row.line}\`](${row.file}:${row.line})`).join(', ')}`,
  30,
)}
### Dead-Code Candidates

Owner: database/backend. Classification rule: remove only after confirming no handler, worker, CLI, migration test, or planned compatibility path uses the query.

${bulletList(inventory.sqlQueriesWithoutExternalUse, (item) =>
  `\`${item.name}\` declared at [\`${item.file}:${item.line}\`](${item.file}:${item.line}) has no non-generated Go reference`,
)}
Owner: frontend/platform. Classification rule: verify relative imports and dynamic imports before removal.

${bulletList(inventory.componentImportCandidates, (item) =>
  `\`${item.path}\` (${item.file}) has no absolute \`@/components/...\` import`,
)}
Owner: deployment/platform. Classification rule: keep if consumed by tests, docs, subcharts, or future production overrides; otherwise remove from values and schema together.

${bulletList(inventory.helmTopLevelValueCandidates, (item) =>
  `\`${item}\` is a top-level \`deploy/chart/values.yaml\` key with no direct template reference`,
)}
## Summary

- Hard failures: ${hardFailures}
- Duplicate-code candidates: ${duplicateCandidateCount}
- Dead-code candidates: ${deadCodeCandidateCount}

## Definition Of Done For Each Candidate

- The owner verifies runtime usage with search, tests, and any generated artifacts.
- Removal candidates include focused tests or snapshots proving behavior is unchanged.
- Consolidation candidates name the target package/component before code moves.
- Keep decisions include a compatibility or ownership reason in the relevant doc or code comment.
`;
}

function buildInventory() {
  const feFiles = frontendFiles();
  const internalGoFiles = goFiles();
  const goRefs = goReferenceFiles();
  const sqlQueries = sqlQueryDeclarations();
  const componentCandidates = componentImportCandidates(feFiles);
  const helmCandidates = helmTopLevelValueCandidates();
  return {
    frontendFileCount: feFiles.length,
    frontendLineCount: countLines(feFiles),
    goFileCount: internalGoFiles.length,
    goReferenceFileCount: goRefs.length,
    sqlQueryCount: sqlQueries.length,
    componentFileCount: walk(path.join(repoRoot, 'frontend/src/components'), (file) =>
      ['.ts', '.tsx'].includes(path.extname(file)) &&
      !rel(file).includes('/__tests__/'),
    ).length,
    helmTopLevelValueCount: read(path.join(repoRoot, 'deploy/chart/values.yaml'))
      .split(/\r?\n/)
      .filter((line) => /^[A-Za-z][A-Za-z0-9_-]*:\s*/.test(line)).length,
    duplicateDependencies: packageDependencyDuplicates(),
    directHttpCallSites: directHttpCallSites(feFiles),
    directToastCallSites: directToastCallSites(feFiles),
    localResponseShapes: localResponseShapes(feFiles),
    duplicateFrontendApiShapeTypes: duplicateFrontendApiShapeTypes(feFiles),
    localStatusBadgeComponents: localStatusBadgeComponents(feFiles),
    rawTableTagCallSites: rawTableTagCallSites(feFiles),
    rawOverlayCallSites: rawOverlayCallSites(feFiles),
    authSessionLiteralCallSites: authSessionLiteralCallSites(feFiles),
    pageQueryKeys: pageQueryKeys(feFiles),
    duplicateGoHelpers: duplicateUnexportedGoFunctions(internalGoFiles),
    sqlQueriesWithoutExternalUse: sqlQueriesWithoutExternalUse(sqlQueries, goRefs),
    componentImportCandidates: componentCandidates,
    helmTopLevelValueCandidates: helmCandidates,
  };
}

const inventory = buildInventory();
const markdown = renderMarkdown(inventory);
const outputPath = path.join(repoRoot, 'docs/rancher-quality-phase0-code-health-inventory.md');

if (args.has('--write')) {
  fs.writeFileSync(outputPath, markdown);
}

if (args.has('--verify-doc')) {
  const current = exists(outputPath) ? read(outputPath) : '';
  if (current !== markdown) {
    console.error('docs/rancher-quality-phase0-code-health-inventory.md is stale.');
    console.error('Run: node scripts/code-health-inventory.mjs --write');
    process.exitCode = 1;
  }
}

if (args.has('--check')) {
  const failures = [
    ...inventory.duplicateDependencies.map((item) => `duplicate dependency: ${item.name}`),
    ...inventory.directHttpCallSites.map((item) => `direct HTTP call: ${item.file}:${item.line}`),
    ...inventory.directToastCallSites.map((item) => `direct toast call: ${item.file}:${item.line}`),
    ...inventory.localResponseShapes.map((item) => `local ResponseShape: ${item.file}:${item.line}`),
    ...inventory.duplicateFrontendApiShapeTypes.map((item) => `duplicate frontend API shape: ${item.name}`),
    ...inventory.localStatusBadgeComponents.map((item) => `local StatusBadge: ${item.file}:${item.line}`),
    ...inventory.rawTableTagCallSites.map((item) => `raw table tag: ${item.file}:${item.line}`),
    ...inventory.rawOverlayCallSites.map((item) => `raw overlay: ${item.file}:${item.line}`),
    ...inventory.authSessionLiteralCallSites.map((item) => `auth session literal: ${item.file}:${item.line}`),
    ...inventory.pageQueryKeys.map((item) => `page-local query key: ${item.file}:${item.line}`),
  ];
  if (failures.length > 0) {
    console.error('Code-health hard gates failed:');
    for (const failure of failures) console.error(`- ${failure}`);
    process.exitCode = 1;
  }
}

if (!args.has('--write') && !args.has('--verify-doc')) {
  process.stdout.write(markdown);
}
