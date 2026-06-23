import {
  appendArrayItem,
  dumpHelmValuesYAML,
  getValueAtPath,
  hasRenderableSchema,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  removeArrayItem,
  resolveSchemaRefs,
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

describe('resolveSchemaRefs', () => {
  it('inlines a top-level $ref into $defs (cert-manager shape)', () => {
    const raw = {
      $ref: '#/$defs/helm-values',
      $defs: {
        'helm-values': {
          type: 'object',
          properties: {
            replicaCount: { type: 'number' },
            image: { $ref: '#/$defs/image' },
          },
        },
        image: { type: 'object', properties: { tag: { type: 'string' } } },
      },
    };
    const out = resolveSchemaRefs(raw)!;
    expect(hasRenderableSchema(out)).toBe(true);
    expect(out.properties!.replicaCount.type).toBe('number');
    // nested $ref resolved too
    expect((out.properties!.image.properties as Record<string, { type: string }>).tag.type).toBe('string');
    // $defs stripped from output
    expect((out as Record<string, unknown>).$defs).toBeUndefined();
  });

  it('survives a self-referential cycle', () => {
    const raw = { $ref: '#/$defs/node', $defs: { node: { type: 'object', properties: { child: { $ref: '#/$defs/node' } } } } };
    const out = resolveSchemaRefs(raw)!;
    expect(out.properties!.child).toBeDefined(); // opaque, but no infinite loop
  });
});
