#!/usr/bin/env node
/**
 * P7.1 — OpenAPI-derived stub bodies for the route-smoke crawl.
 *
 * Reads the repo contract (`docs/openapi.yaml`) and emits one minimal 200
 * body per documented `/api/v1/**` GET path: arrays -> [], objects -> every
 * declared property at its type-zero value, enums -> first member. Bodies are
 * emitted exactly as the spec spells them — snake_case wire format — because
 * the axios camelize interceptor expects wire shapes.
 *
 * Emitting ALL declared properties (not just `required`) is deliberate: the
 * handlers' envelope (`{ data: ... }`) and pagination shells rarely mark
 * anything required, and a `{}` body would turn every consumer's
 * `res.data.data` into undefined — the point of the crawl is to render pages
 * over skeleton data, not to crash them on a missing envelope.
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(frontendRoot, '..');
// Same createRequire trick as scripts/generate-openapi-types.mjs: js-yaml is
// a frontend dependency, resolved from frontend/node_modules.
const requireFromFrontend = createRequire(path.join(frontendRoot, 'package.json'));
const yaml = requireFromFrontend('js-yaml');

const specPath = path.join(repoRoot, 'docs/openapi.yaml');
const outputPath = path.join(frontendRoot, 'tests/e2e-smoke/openapi-stubs.generated.json');
const spec = yaml.load(fs.readFileSync(specPath, 'utf8'));

if (!spec?.paths || typeof spec.paths !== 'object') {
  console.error('generate-e2e-stubs: OpenAPI spec has no paths object');
  process.exit(1);
}

function resolveRef(ref) {
  if (!ref.startsWith('#/')) return undefined;
  let node = spec;
  for (const part of ref.slice(2).split('/')) {
    node = node?.[part];
  }
  return node;
}

/** Build a type-zero instance of an (OpenAPI 3.1) schema. */
function zeroValue(schema, seenRefs = new Set()) {
  if (!schema || typeof schema !== 'object') return null;
  if (schema.$ref) {
    if (seenRefs.has(schema.$ref)) return null; // cycle guard
    const resolved = resolveRef(schema.$ref);
    return zeroValue(resolved, new Set([...seenRefs, schema.$ref]));
  }
  if (Array.isArray(schema.allOf)) {
    const merged = {};
    for (const member of schema.allOf) {
      const value = zeroValue(member, seenRefs);
      if (value && typeof value === 'object' && !Array.isArray(value)) Object.assign(merged, value);
    }
    return merged;
  }
  if (Array.isArray(schema.oneOf) && schema.oneOf.length > 0) return zeroValue(schema.oneOf[0], seenRefs);
  if (Array.isArray(schema.anyOf) && schema.anyOf.length > 0) return zeroValue(schema.anyOf[0], seenRefs);
  if (Array.isArray(schema.enum) && schema.enum.length > 0) return schema.enum[0];
  if (schema.const !== undefined) return schema.const;

  // 3.1 union types (e.g. [string, 'null']): use the first non-null member.
  let type = schema.type;
  if (Array.isArray(type)) type = type.find((t) => t !== 'null') ?? 'null';
  if (!type && schema.properties) type = 'object';
  if (!type && schema.items) type = 'array';

  switch (type) {
    case 'object': {
      const out = {};
      for (const [prop, propSchema] of Object.entries(schema.properties ?? {})) {
        out[prop] = zeroValue(propSchema, seenRefs);
      }
      return out;
    }
    case 'array':
      return [];
    case 'string':
      if (schema.format === 'date-time') return '1970-01-01T00:00:00Z';
      if (schema.format === 'date') return '1970-01-01';
      return '';
    case 'integer':
    case 'number':
      return 0;
    case 'boolean':
      return false;
    case 'null':
      return null;
    default:
      return null;
  }
}

function successResponseSchema(getOp) {
  const responses = getOp?.responses ?? {};
  for (const code of ['200', '201', '2XX', 'default']) {
    let response = responses[code];
    if (!response) continue;
    if (response.$ref) response = resolveRef(response.$ref);
    const schema = response?.content?.['application/json']?.schema;
    if (schema) return schema;
  }
  return undefined;
}

const stubs = {};
let skipped = 0;
for (const [rawPath, item] of Object.entries(spec.paths)) {
  const apiPath = rawPath.trim();
  if (!apiPath.startsWith('/api/v1/')) continue; // scim/internal are not called by the SPA
  const getOp = item?.get;
  if (!getOp) continue;
  const schema = successResponseSchema(getOp);
  if (!schema) {
    skipped += 1;
    continue;
  }
  stubs[apiPath] = zeroValue(schema);
}

const count = Object.keys(stubs).length;
if (count < 100) {
  // Anti-vacuous floor (R7): the spec documents ~200 GETs today; a collapse
  // to a handful means the parser or the spec layout changed under us.
  console.error(`generate-e2e-stubs: only ${count} GET stubs generated — spec parse looks broken`);
  process.exit(1);
}

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, JSON.stringify(stubs, null, 2) + '\n');
console.log(
  `e2e-stubs: ${count} GET stubs -> ${path.relative(frontendRoot, outputPath)}` +
    (skipped ? ` (${skipped} GETs without a JSON success schema skipped)` : ''),
);
