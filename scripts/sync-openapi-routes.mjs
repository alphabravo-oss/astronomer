#!/usr/bin/env node
// Keep the public OpenAPI path inventory aligned with the mounted chi router.
//
// This is intentionally a text-preserving generator: docs/openapi.yaml contains
// carefully maintained descriptions and examples, so parsing and dumping the
// entire document through a YAML serializer would create a noisy, lossy diff.
// Existing operations only receive a stable operationId when one is absent;
// newly mounted operations receive a complete, conservative contract skeleton
// that makes their auth, route class, path parameters, body and responses
// explicit. Domain owners can then tighten the generated generic wire body
// without losing the route-drift guarantee.

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const requireFromFrontend = createRequire(path.join(repoRoot, 'frontend/package.json'));
const yaml = requireFromFrontend('js-yaml');

const args = new Set(process.argv.slice(2));
const write = args.has('--write');
const check = args.has('--check');
if (write === check) {
  console.error('Usage: node scripts/sync-openapi-routes.mjs --write|--check');
  process.exit(2);
}

const specPath = path.join(repoRoot, 'docs/openapi.yaml');
const routesPath = path.join(repoRoot, 'docs/routes.json');
const riskPath = path.join(repoRoot, 'docs/security-sensitive-routes.json');
const methods = new Set(['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace']);

const normalizePath = (value) => {
  let result = value.replace(/\{[^}]*\}/g, '{}');
  if (result.length > 1) result = result.replace(/\/+$/, '');
  return result;
};
const routeKey = (method, routePath) => `${method.toUpperCase()} ${normalizePath(routePath)}`;

function words(value) {
  return value
    .replace(/^\/+|\/+$/g, '')
    .split('/')
    .flatMap((segment) => {
      const match = /^\{([^}]+)\}$/.exec(segment);
      if (match) return ['by', ...match[1].replace(/^\.+/, '').split(/[-_]+/g)];
      if (segment === '*') return ['proxy'];
      return segment.split(/[-_.]+/g);
    })
    .filter(Boolean);
}

function pascal(value) {
  return value ? value[0].toUpperCase() + value.slice(1) : '';
}

function operationId(method, routePath) {
  const withoutPrefix = routePath
    .replace(/^\/api\/v1\/?/, '')
    .replace(/^\/scim\/v2\/?/, 'scim/');
  return method.toLowerCase() + words(withoutPrefix).map(pascal).join('');
}

function classify(routePath) {
  if (routePath === '/health') return 'diagnostic';
  if (routePath.startsWith('/internal/')) return 'internal';
  if (routePath.startsWith('/scim/')) return 'scim';
  if (routePath.includes('/ws/') || /\/(watch|stream|connect|events\/stream)(\/|$)/.test(routePath)) return 'stream';
  if (isPassthroughRoute(routePath) || routePath.endsWith('/*')) return 'proxy';
  if (/(export|\.csv|kubeconfig|support-bundle|diagnostics\/bundle)/.test(routePath)) return 'download';
  if (routePath.startsWith('/api/v1/alerts/')) return 'compatibility-alias';
  return 'public-api';
}

function isPassthroughRoute(routePath) {
  return (routePath.startsWith('/api/v1/clusters/') && routePath.includes('/k8s/')) ||
    routePath.includes('/proxy/service/') ||
    routePath === '/api/v1/clusters/{cluster_id}/resources/{resource_type}' ||
    routePath === '/api/v1/resources/{cluster_id}/{type}/{namespace}/{name}';
}

function isPolymorphicRoute(routePath) {
  return routePath.replace(/\/+$/, '') === '/api/v1/clusters/{cluster_id}/service-mesh/validate';
}

function tagFor(routePath) {
  const parts = routePath.replace(/^\/api\/v1\//, '').replace(/^\/scim\/v2\//, 'scim/').split('/');
  const domain = parts[0] === 'admin' && parts[1] ? parts[1] : parts[0];
  return (domain || 'Platform').split(/[-_]+/).map(pascal).join('');
}

function summaryFor(method, routePath) {
  const action = { get: 'Read', post: 'Create or execute', put: 'Replace', patch: 'Update', delete: 'Delete' }[method] || method.toUpperCase();
  return `${action} ${routePath.replace(/^\/api\/v1\//, '').replace(/^\//, '')}`;
}

function yamlQuote(value) {
  return JSON.stringify(value);
}

function responseLines(method, routePath) {
  const routeClass = classify(routePath);
  if (method === 'delete') {
    return ["        '204':", '          description: Resource deleted or already absent'];
  }
  if (routeClass === 'download') {
    const csv = routePath.includes('.csv') || routePath.endsWith('/export');
    const mediaType = csv ? 'text/csv' : routePath.includes('kubeconfig') ? 'application/yaml' : 'application/octet-stream';
    return [
      "        '200':",
      '          description: Audited download payload',
      '          content:',
      `            ${mediaType}:`,
      '              schema: {type: string, format: binary}',
    ];
  }
  const status = method === 'post' ? '200' : '200';
  return [
    `        '${status}':`,
    '          description: Successful operation',
    '          content:',
    '            application/json:',
    "              schema: {$ref: '#/components/schemas/RouteResponseEnvelope'}",
  ];
}

function riskByRoute() {
  const entries = JSON.parse(fs.readFileSync(riskPath, 'utf8'));
  const map = new Map();
  for (const entry of entries) {
    if (!entry.method || entry.method === '*') continue;
    map.set(routeKey(entry.method, entry.route), entry);
    map.set(routeKey(entry.method, entry.sample_path), entry);
  }
  return map;
}

function generatedOperation(route, risk, contractPath = route.pattern) {
  const method = route.method.toLowerCase();
  const routeClass = classify(route.pattern);
  const lines = [
    `    ${method}:`,
    `      operationId: ${operationId(method, contractPath)}`,
    `      tags: [${tagFor(route.pattern)}]`,
    `      summary: ${yamlQuote(summaryFor(method, route.pattern))}`,
    '      description: Contract generated from the mounted route inventory; response and request envelopes are stable even when domain payload fields evolve additively.',
    `      x-astronomer-route-class: ${routeClass}`,
  ];
  if (risk?.rbac) lines.push(`      x-astronomer-rbac: ${yamlQuote(risk.rbac)}`);
  else lines.push('      x-astronomer-rbac: "handler-and-route-policy"');
  if (risk?.auth) lines.push(`      x-astronomer-auth: ${yamlQuote(risk.auth)}`);
  else lines.push(`      x-astronomer-auth: ${yamlQuote(route.pattern === '/health' ? 'public' : 'bearer-or-session')}`);

  const params = [...contractPath.matchAll(/\{([^}]+)\}/g)].map((match) => match[1].replace(/^\.+/, ''));
  if (params.length) {
    lines.push('      parameters:');
    for (const param of params) {
      lines.push(`        - {name: ${param}, in: path, required: true, schema: {type: string, minLength: 1}}`);
    }
  }

  if (['post', 'put', 'patch'].includes(method)) {
    const requestStatus = isPassthroughRoute(route.pattern)
      ? 'passthrough'
      : isPolymorphicRoute(route.pattern)
        ? 'polymorphic'
        : 'provisional';
    lines.push(`      x-astronomer-request-schema-status: ${requestStatus}`);
    lines.push('      requestBody:');
    lines.push(`        required: ${method === 'patch' || method === 'put'}`);
    lines.push('        content:');
    lines.push('          application/json:');
    if (requestStatus === 'provisional') {
      lines.push("            schema: {$ref: '#/components/schemas/RouteMutationRequest'}");
    } else {
      lines.push('            schema: {}');
    }
  }

  if (route.pattern === '/health') lines.push('      security: []');
  lines.push('      responses:');
  lines.push(...responseLines(method, route.pattern));
  if (route.pattern !== '/health') {
    lines.push("        '400': {$ref: '#/components/responses/BadRequest'}");
    lines.push("        '401': {$ref: '#/components/responses/Unauthorized'}");
    lines.push("        '403': {$ref: '#/components/responses/Forbidden'}");
    lines.push("        '404': {$ref: '#/components/responses/NotFound'}");
    lines.push("        '503': {$ref: '#/components/responses/ServiceUnavailable'}");
  }
  return lines;
}

function ensureOperationMetadata(source) {
  const lines = source.split('\n');
  const pathsAt = lines.findIndex((line) => line === 'paths:');
  if (pathsAt < 0) throw new Error('OpenAPI document has no top-level paths mapping');
  let currentPath = '';
  const inserts = [];
  const risks = riskByRoute();
  for (let i = pathsAt + 1; i < lines.length; i += 1) {
    const line = lines[i];
    const pathMatch = /^  (\/[^:]*):\s*$/.exec(line);
    if (pathMatch) {
      currentPath = pathMatch[1];
      continue;
    }
    const methodMatch = /^    (get|put|post|delete|patch|head|options|trace):\s*$/.exec(line);
    if (!methodMatch || !currentPath) continue;
    let end = i + 1;
    while (end < lines.length && !/^  \/|^    (get|put|post|delete|patch|head|options|trace):/.test(lines[end])) end += 1;
    const block = lines.slice(i + 1, end);
    const additions = [];
    if (!block.some((candidate) => /^      operationId:/.test(candidate))) {
      additions.push(`      operationId: ${operationId(methodMatch[1], currentPath)}`);
    }
    const requestStatusIndex = block.findIndex((candidate) => /^      x-astronomer-request-schema-status:/.test(candidate));
    const stalePassthrough = requestStatusIndex >= 0 &&
      block[requestStatusIndex] === '      x-astronomer-request-schema-status: passthrough' &&
      !isPassthroughRoute(currentPath);
    if (stalePassthrough) {
      lines[i + 1 + requestStatusIndex] = '';
      block[requestStatusIndex] = '';
    }
    const routeClassIndex = block.findIndex((candidate) => /^      x-astronomer-route-class:/.test(candidate));
    if (routeClassIndex < 0) {
      additions.push(`      x-astronomer-route-class: ${classify(currentPath)}`);
    } else if ((isPassthroughRoute(currentPath) || stalePassthrough) && block[routeClassIndex] !== `      x-astronomer-route-class: ${classify(currentPath)}`) {
      block[routeClassIndex] = `      x-astronomer-route-class: ${classify(currentPath)}`;
      lines[i + 1 + routeClassIndex] = block[routeClassIndex];
    }
    const hasJSONRequestBody = block.some((candidate) => /^      requestBody:/.test(candidate));
    if (isPassthroughRoute(currentPath) && hasJSONRequestBody &&
        !block.some((candidate) => /^      x-astronomer-request-schema-status:/.test(candidate))) {
      additions.push('      x-astronomer-request-schema-status: passthrough');
    }
    if (isPolymorphicRoute(currentPath) && hasJSONRequestBody &&
        !block.some((candidate) => /^      x-astronomer-request-schema-status:/.test(candidate))) {
      additions.push('      x-astronomer-request-schema-status: polymorphic');
    }
    if (!block.some((candidate) => /^      x-(astronomer-)?rbac:/.test(candidate))) {
      const risk = risks.get(routeKey(methodMatch[1], currentPath));
      const isPublic = currentPath === '/health' || block.some((candidate) => /^      security: \[\]/.test(candidate));
      additions.push(`      x-astronomer-rbac: ${yamlQuote(risk?.rbac || (isPublic ? 'public' : 'handler-and-route-policy'))}`);
    }
    if (!block.some((candidate) => /^      x-astronomer-auth:/.test(candidate))) {
      const risk = risks.get(routeKey(methodMatch[1], currentPath));
      const isPublic = currentPath === '/health' || block.some((candidate) => /^      security: \[\]/.test(candidate));
      additions.push(`      x-astronomer-auth: ${yamlQuote(risk?.auth || (isPublic ? 'public' : 'bearer-or-session'))}`);
    }
    for (const lineToInsert of additions.reverse()) inserts.push({ at: i + 1, line: lineToInsert });
  }
  for (const insert of inserts.reverse()) lines.splice(insert.at, 0, insert.line);
  return lines.join('\n');
}

function generate(source) {
  source = ensureOperationMetadata(source);
  const spec = yaml.load(source);
  const existing = new Set();
  const contractPathByNormalizedPath = new Map();
  for (const [routePath, item] of Object.entries(spec.paths || {})) {
    if (!contractPathByNormalizedPath.has(normalizePath(routePath))) {
      contractPathByNormalizedPath.set(normalizePath(routePath), routePath);
    }
    for (const method of Object.keys(item || {})) {
      if (methods.has(method.toLowerCase())) existing.add(routeKey(method, routePath));
    }
  }

  const risks = riskByRoute();
  const uniqueRoutes = new Map();
  for (const route of JSON.parse(fs.readFileSync(routesPath, 'utf8'))) {
    if (route.method.toUpperCase() === 'CONNECT') continue;
    uniqueRoutes.set(routeKey(route.method, route.pattern), route);
  }
  const routes = [...uniqueRoutes.values()]
    .filter((route) => !existing.has(routeKey(route.method, route.pattern)))
    .sort((a, b) => a.pattern.localeCompare(b.pattern) || a.method.localeCompare(b.method));

  if (!routes.length) return source;
  const newPathLines = [];
  const operationsForExistingPath = new Map();
  let lastPath = '';
  for (const route of routes) {
    const contractPath = contractPathByNormalizedPath.get(normalizePath(route.pattern));
    if (contractPath) {
      const operations = operationsForExistingPath.get(contractPath) || [];
      operations.push(...generatedOperation(route, risks.get(routeKey(route.method, route.pattern)), contractPath));
      operationsForExistingPath.set(contractPath, operations);
      continue;
    }
    if (route.pattern !== lastPath) {
      newPathLines.push(`  ${route.pattern}:`);
      lastPath = route.pattern;
    }
    newPathLines.push(...generatedOperation(route, risks.get(routeKey(route.method, route.pattern))));
  }

  const marker = '\npaths:\n';
  const pathsAt = source.indexOf(marker);
  if (pathsAt < 0) throw new Error('OpenAPI document has no paths marker');
  if (newPathLines.length) {
    const insertionAt = pathsAt + marker.length;
    source = source.slice(0, insertionAt) + newPathLines.join('\n') + '\n' + source.slice(insertionAt);
  }

  // Insert additional methods into an existing Path Item rather than creating
  // a second YAML mapping key for the same path.
  for (const [contractPath, operationLines] of operationsForExistingPath) {
    const pathMarker = `\n  ${contractPath}:\n`;
    const pathAt = source.indexOf(pathMarker);
    if (pathAt < 0) throw new Error(`Cannot locate existing path item ${contractPath}`);
    const itemStart = pathAt + pathMarker.length;
    const nextPath = source.indexOf('\n  /', itemStart);
    const insertAt = nextPath < 0 ? source.length : nextPath;
    source = source.slice(0, insertAt) + '\n' + operationLines.join('\n') + source.slice(insertAt);
  }
  return source;
}

const current = fs.readFileSync(specPath, 'utf8');
const generated = generate(current);

if (write) {
  fs.writeFileSync(specPath, generated);
  console.log(`Synchronized ${path.relative(repoRoot, specPath)} with ${path.relative(repoRoot, routesPath)}.`);
  process.exit(0);
}

if (current !== generated) {
  console.error('docs/openapi.yaml is not synchronized with the mounted route inventory.');
  console.error('Run: node scripts/sync-openapi-routes.mjs --write');
  process.exit(1);
}

console.log('OpenAPI routes and stable operationIds are synchronized.');
