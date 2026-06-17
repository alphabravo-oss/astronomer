#!/usr/bin/env node
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
  console.error('Usage: node scripts/generate-openapi-types.mjs --write|--check');
  process.exit(2);
}

const specPath = path.join(repoRoot, 'docs/openapi.yaml');
const outputPath = path.join(repoRoot, 'frontend/src/types/openapi.generated.ts');
const spec = yaml.load(fs.readFileSync(specPath, 'utf8'));
const schemas = spec?.components?.schemas;

if (!schemas || typeof schemas !== 'object' || Array.isArray(schemas)) {
  console.error('OpenAPI spec has no components.schemas object');
  process.exit(1);
}

// validateSchemaRefs walks the ENTIRE spec (paths, responses, nested schemas —
// not just components.schemas) and fails if any `#/components/schemas/<name>`
// $ref targets a schema that is never defined. The type emitter below only
// iterates components.schemas, so on its own it cannot observe dangling refs
// from path operations; this guard closes that gap so `--check` reflects spec
// validity rather than merely "the emitted types are up to date".
function validateSchemaRefs(root, defined) {
  const prefix = '#/components/schemas/';
  const dangling = new Set();
  const visit = (node) => {
    if (Array.isArray(node)) {
      for (const item of node) visit(item);
      return;
    }
    if (!node || typeof node !== 'object') return;
    for (const [key, value] of Object.entries(node)) {
      if (key === '$ref' && typeof value === 'string' && value.startsWith(prefix)) {
        const name = value.slice(prefix.length);
        if (!Object.prototype.hasOwnProperty.call(defined, name)) dangling.add(name);
      } else {
        visit(value);
      }
    }
  };
  visit(root);
  if (dangling.size > 0) {
    console.error('OpenAPI spec references schemas that are not defined in components.schemas:');
    for (const name of [...dangling].sort()) console.error(`  - ${name}`);
    process.exit(1);
  }
}

function assertIdentifier(name) {
  if (!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name)) {
    throw new Error(`OpenAPI schema name ${JSON.stringify(name)} is not a valid TypeScript identifier`);
  }
}

function literal(value) {
  return JSON.stringify(value);
}

function indent(text, spaces = 2) {
  const pad = ' '.repeat(spaces);
  return text
    .split('\n')
    .map((line) => (line ? `${pad}${line}` : line))
    .join('\n');
}

function refName(ref) {
  const prefix = '#/components/schemas/';
  if (typeof ref !== 'string' || !ref.startsWith(prefix)) {
    throw new Error(`Unsupported OpenAPI $ref ${JSON.stringify(ref)}`);
  }
  const name = ref.slice(prefix.length);
  assertIdentifier(name);
  return `OpenAPIComponents['schemas']['${name}']`;
}

function union(parts) {
  const unique = [...new Set(parts)];
  if (unique.length === 0) return 'unknown';
  if (unique.length === 1) return unique[0];
  return unique.join(' | ');
}

function objectType(schema, level) {
  const properties = schema.properties && typeof schema.properties === 'object'
    ? schema.properties
    : {};
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  const entries = Object.entries(properties);
  const additional = schema.additionalProperties;

  if (entries.length === 0) {
    if (additional === false) return 'Record<string, never>';
    if (additional === true || additional === undefined) return 'Record<string, unknown>';
    return `Record<string, ${typeForSchema(additional, level)}>`;
  }

  const lines = ['{'];
  for (const [key, value] of entries) {
    const optional = required.has(key) ? '' : '?';
    lines.push(`${' '.repeat(level + 2)}${literal(key)}${optional}: ${typeForSchema(value, level + 2)};`);
  }
  lines.push(`${' '.repeat(level)}}`);

  const base = lines.join('\n');
  if (additional === false || additional === undefined) return base;
  if (additional === true) return `${base} & Record<string, unknown>`;
  return `${base} & Record<string, ${typeForSchema(additional, level)}>`;
}

function typeForSchema(schema, level = 0) {
  if (!schema || typeof schema !== 'object') return 'unknown';
  if (schema.$ref) return withNullable(refName(schema.$ref), schema);
  if (Array.isArray(schema.allOf)) {
    return withNullable(schema.allOf.map((item) => typeForSchema(item, level)).join(' & '), schema);
  }
  if (Array.isArray(schema.anyOf)) {
    return withNullable(union(schema.anyOf.map((item) => typeForSchema(item, level))), schema);
  }
  if (Array.isArray(schema.oneOf)) {
    return withNullable(union(schema.oneOf.map((item) => typeForSchema(item, level))), schema);
  }
  if (Array.isArray(schema.enum)) {
    return withNullable(union(schema.enum.map((value) => literal(value))), schema);
  }

  const rawType = Array.isArray(schema.type)
    ? schema.type.filter((type) => type !== 'null')
    : [schema.type ?? (schema.properties ? 'object' : undefined)];
  const nullable = schema.nullable === true || (Array.isArray(schema.type) && schema.type.includes('null'));
  const mapped = rawType.map((type) => {
    switch (type) {
      case 'string':
        return 'string';
      case 'integer':
      case 'number':
        return 'number';
      case 'boolean':
        return 'boolean';
      case 'array': {
        const itemType = typeForSchema(schema.items, level);
        return needsParens(itemType) ? `Array<${itemType}>` : `${itemType}[]`;
      }
      case 'object':
        return objectType(schema, level);
      case undefined:
        if (schema.additionalProperties) return objectType(schema, level);
        return 'unknown';
      default:
        throw new Error(`Unsupported OpenAPI schema type ${JSON.stringify(type)}`);
    }
  });

  return nullable ? union([...mapped, 'null']) : union(mapped);
}

function needsParens(type) {
  return type.includes(' | ') || type.includes(' & ') || type.includes('\n');
}

function withNullable(type, schema) {
  return schema.nullable === true ? union([type, 'null']) : type;
}

function generate() {
  const names = Object.keys(schemas).sort();
  for (const name of names) assertIdentifier(name);

  const lines = [
    '// Generated by scripts/generate-openapi-types.mjs from docs/openapi.yaml.',
    '// Do not edit by hand; run `npm run openapi:types` from frontend/ instead.',
    '',
    'export interface OpenAPIComponents {',
    '  schemas: {',
  ];

  for (const name of names) {
    lines.push(`    ${name}: ${indent(typeForSchema(schemas[name], 4), 4).trimStart()};`);
  }

  lines.push('  };');
  lines.push('}');
  lines.push('');
  lines.push("export type OpenAPISchemaName = keyof OpenAPIComponents['schemas'];");
  lines.push('');

  for (const name of names) {
    lines.push(`export type ${name} = OpenAPIComponents['schemas']['${name}'];`);
  }

  lines.push('');
  return lines.join('\n');
}

validateSchemaRefs(spec, schemas);

const generated = generate();

if (write) {
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });
  fs.writeFileSync(outputPath, generated);
  process.exit(0);
}

const current = fs.existsSync(outputPath) ? fs.readFileSync(outputPath, 'utf8') : '';
if (current !== generated) {
  console.error('frontend/src/types/openapi.generated.ts is out of date.');
  console.error('Run `cd frontend && npm run openapi:types`.');
  process.exit(1);
}
