import yaml from 'js-yaml';

export interface HelmValuesSchemaNode {
  type?: string | string[];
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  properties?: Record<string, HelmValuesSchemaNode>;
  items?: HelmValuesSchemaNode;
  required?: string[];
}

export type HelmValuesObject = Record<string, unknown>;

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function schemaType(schema: HelmValuesSchemaNode): string {
  if (Array.isArray(schema.type)) {
    return schema.type.find((t) => t !== 'null') || schema.type[0] || 'object';
  }
  if (schema.type) return schema.type;
  if (schema.properties) return 'object';
  if (schema.items) return 'array';
  return 'string';
}

function cloneValue<T>(value: T): T {
  if (typeof structuredClone === 'function') return structuredClone(value);
  return JSON.parse(JSON.stringify(value)) as T;
}

export function parseHelmValuesYAML(raw: string): HelmValuesObject | null {
  if (!raw.trim()) return {};
  const parsed = yaml.load(raw);
  if (parsed == null) return {};
  return isPlainObject(parsed) ? (parsed as HelmValuesObject) : null;
}

export function dumpHelmValuesYAML(values: HelmValuesObject): string {
  return yaml.dump(values, { lineWidth: -1, noRefs: true }).trim();
}

export function defaultValueForSchema(schema: HelmValuesSchemaNode): unknown {
  if (schema.default !== undefined) return cloneValue(schema.default);
  if (schema.enum && schema.enum.length > 0) return schema.enum[0];
  switch (schemaType(schema)) {
    case 'object':
      return {};
    case 'array':
      return [];
    case 'boolean':
      return false;
    case 'integer':
    case 'number':
      return 0;
    default:
      return '';
  }
}

export function mergeSchemaDefaults(schema: HelmValuesSchemaNode, value: unknown): unknown {
  if (value === undefined || value === null) {
    value = defaultValueForSchema(schema);
  }

  switch (schemaType(schema)) {
    case 'object': {
      const base = isPlainObject(value) ? { ...value } : {};
      for (const [key, childSchema] of Object.entries(schema.properties || {})) {
        const merged = mergeSchemaDefaults(childSchema, base[key]);
        if (merged !== undefined) {
          base[key] = merged;
        }
      }
      return base;
    }
    case 'array': {
      if (!Array.isArray(value)) {
        return Array.isArray(schema.default) ? cloneValue(schema.default) : [];
      }
      if (!schema.items) return value;
      return value.map((item) => mergeSchemaDefaults(schema.items as HelmValuesSchemaNode, item));
    }
    case 'boolean':
      return typeof value === 'boolean' ? value : Boolean(value);
    case 'integer':
    case 'number':
      return typeof value === 'number' ? value : schema.default ?? 0;
    default:
      return typeof value === 'string' ? value : String(value ?? schema.default ?? '');
  }
}

export function setValueAtPath(root: HelmValuesObject, path: string[], nextValue: unknown): HelmValuesObject {
  if (path.length === 0) return root;
  const nextRoot = cloneValue(root);
  let cursor: unknown = nextRoot;
  for (let i = 0; i < path.length - 1; i += 1) {
    const segment = path[i];
    const nextSegment = path[i + 1];

    if (Array.isArray(cursor)) {
      const index = Number(segment);
      const child = cursor[index];
      if (child == null) {
        cursor[index] = Number.isInteger(Number(nextSegment)) ? [] : {};
      }
      cursor = cursor[index];
      continue;
    }

    if (!isPlainObject(cursor)) {
      throw new Error(`invalid path segment ${segment}`);
    }
    const child = cursor[segment];
    if (child == null) {
      cursor[segment] = Number.isInteger(Number(nextSegment)) ? [] : {};
    }
    cursor = cursor[segment];
  }

  const leaf = path[path.length - 1];
  if (Array.isArray(cursor)) {
    cursor[Number(leaf)] = nextValue;
  } else if (isPlainObject(cursor)) {
    cursor[leaf] = nextValue;
  } else {
    throw new Error(`invalid leaf segment ${leaf}`);
  }
  return nextRoot;
}

export function appendArrayItem(root: HelmValuesObject, path: string[], schema: HelmValuesSchemaNode): HelmValuesObject {
  const current = getValueAtPath(root, path);
  const next = Array.isArray(current) ? [...current] : [];
  next.push(mergeSchemaDefaults(schema.items || {}, undefined));
  return setValueAtPath(root, path, next);
}

export function removeArrayItem(root: HelmValuesObject, path: string[], index: number): HelmValuesObject {
  const current = getValueAtPath(root, path);
  if (!Array.isArray(current)) return root;
  const next = current.filter((_, i) => i !== index);
  return setValueAtPath(root, path, next);
}

export function getValueAtPath(root: unknown, path: string[]): unknown {
  let cursor = root;
  for (const segment of path) {
    if (Array.isArray(cursor)) {
      cursor = cursor[Number(segment)];
      continue;
    }
    if (!isPlainObject(cursor)) return undefined;
    cursor = cursor[segment];
  }
  return cursor;
}

// resolveJSONPointer dereferences a local "#/a/b" pointer against the root.
function resolveJSONPointer(root: Record<string, unknown>, ref: string): unknown {
  if (!ref.startsWith('#/')) return undefined;
  const parts = ref
    .slice(2)
    .split('/')
    .map((p) => p.replace(/~1/g, '/').replace(/~0/g, '~'));
  let cursor: unknown = root;
  for (const part of parts) {
    if (!isPlainObject(cursor)) return undefined;
    cursor = (cursor as Record<string, unknown>)[part];
  }
  return cursor;
}

// resolveSchemaRefs inlines local $ref/$defs/definitions so the form walker
// (which only understands properties/items/type) can render schemas generated
// by tools like helm-values-schema-json — cert-manager et al. wrap everything
// in {"$ref":"#/$defs/helm-values","$defs":{...}}. Local-only, cycle-guarded,
// depth-capped. Returns the dereferenced schema (or null if not an object).
export function resolveSchemaRefs(schema: unknown): HelmValuesSchemaNode | null {
  if (!isPlainObject(schema)) return null;
  const root = schema as Record<string, unknown>;

  const resolve = (node: unknown, depth: number, active: Set<string>): unknown => {
    if (depth > 60 || !isPlainObject(node)) return node;
    const obj = node as Record<string, unknown>;

    if (typeof obj.$ref === 'string') {
      const ref = obj.$ref;
      if (active.has(ref)) return {}; // cycle: render as opaque object
      const target = resolveJSONPointer(root, ref);
      if (target === undefined) return {};
      const nextActive = new Set(active).add(ref);
      const resolved = resolve(target, depth + 1, nextActive);
      // Sibling keys alongside $ref win over the resolved node.
      const siblings: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(obj)) {
        if (k === '$ref' || k === '$defs' || k === 'definitions') continue;
        siblings[k] = resolve(v, depth + 1, nextActive);
      }
      return { ...(isPlainObject(resolved) ? resolved : {}), ...siblings };
    }

    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(obj)) {
      if (k === '$defs' || k === 'definitions') continue; // drop after inlining
      out[k] = resolve(v, depth + 1, active);
    }
    return out;
  };

  const result = resolve(root, 0, new Set());
  return isPlainObject(result) ? (result as HelmValuesSchemaNode) : null;
}

export function hasRenderableSchema(schema: unknown): schema is HelmValuesSchemaNode {
  if (!isPlainObject(schema)) return false;
  const s = schema as Record<string, unknown>;
  const props = s.properties;
  return (isPlainObject(props) && Object.keys(props).length > 0) || isPlainObject(s.items);
}
