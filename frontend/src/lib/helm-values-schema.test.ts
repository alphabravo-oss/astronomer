import {
  appendArrayItem,
  dumpHelmValuesYAML,
  getValueAtPath,
  hasRenderableSchema,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  removeArrayItem,
  setValueAtPath,
  type HelmValuesSchemaNode,
} from '@/lib/helm-values-schema';

describe('helm-values-schema helpers', () => {
  const schema: HelmValuesSchemaNode = {
    type: 'object',
    properties: {
      replicaCount: { type: 'integer', default: 2 },
      ingress: {
        type: 'object',
        properties: {
          enabled: { type: 'boolean', default: false },
          hosts: { type: 'array', items: { type: 'string' }, default: ['example.com'] },
        },
      },
    },
  };

  it('parses and serializes YAML values', () => {
    const parsed = parseHelmValuesYAML('replicaCount: 3\ningress:\n  enabled: true\n');
    expect(parsed).toEqual({ replicaCount: 3, ingress: { enabled: true } });

    const dumped = dumpHelmValuesYAML({ replicaCount: 3, ingress: { enabled: true } });
    expect(dumped).toContain('replicaCount: 3');
    expect(dumped).toContain('enabled: true');
  });

  it('merges schema defaults into nested objects', () => {
    expect(mergeSchemaDefaults(schema, { ingress: { enabled: true } })).toEqual({
      replicaCount: 2,
      ingress: {
        enabled: true,
        hosts: ['example.com'],
      },
    });
  });

  it('updates nested paths immutably', () => {
    const start = mergeSchemaDefaults(schema, {}) as Record<string, unknown>;
    const next = setValueAtPath(start, ['ingress', 'enabled'], true);
    expect(next).toEqual({
      replicaCount: 2,
      ingress: { enabled: true, hosts: ['example.com'] },
    });
    expect(start).toEqual({
      replicaCount: 2,
      ingress: { enabled: false, hosts: ['example.com'] },
    });
  });

  it('appends and removes array items', () => {
    const start = mergeSchemaDefaults(schema, {}) as Record<string, unknown>;
    const appended = appendArrayItem(start, ['ingress', 'hosts'], {
      type: 'object',
      properties: {},
      items: { type: 'string' },
    });
    expect(getValueAtPath(appended, ['ingress', 'hosts'])).toEqual(['example.com', '']);

    const removed = removeArrayItem(appended, ['ingress', 'hosts'], 0);
    expect(getValueAtPath(removed, ['ingress', 'hosts'])).toEqual(['']);
  });

  it('recognizes empty vs non-empty schema payloads', () => {
    expect(hasRenderableSchema({})).toBe(false);
    expect(hasRenderableSchema(schema)).toBe(true);
  });
});
