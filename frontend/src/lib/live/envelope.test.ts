import { parseFrame } from '@/lib/live/envelope';

describe('parseFrame', () => {
  it('parses a full envelope and camelizes snake_case data keys deeply', () => {
    const ev = parseFrame(
      JSON.stringify({
        id: 42,
        type: 'cluster.metrics',
        time: '2026-07-15T00:00:00Z',
        data: {
          cluster_id: 'c1',
          cpu_percentage: 12.5,
          nested: { pod_count: 3 },
          items: [{ memory_percentage: 40 }],
        },
      }),
    );
    expect(ev).toEqual({
      id: 42,
      type: 'cluster.metrics',
      time: '2026-07-15T00:00:00Z',
      data: {
        clusterId: 'c1',
        cpuPercentage: 12.5,
        nested: { podCount: 3 },
        items: [{ memoryPercentage: 40 }],
      },
    });
  });

  it('defaults id to 0 for sys.ping frames (no id on the wire)', () => {
    const ev = parseFrame(JSON.stringify({ type: 'sys.ping', time: '2026-07-15T00:00:00Z' }));
    expect(ev).toEqual({ id: 0, type: 'sys.ping', time: '2026-07-15T00:00:00Z', data: undefined });
  });

  it('returns null for non-JSON, empty, and untyped frames', () => {
    expect(parseFrame('not json')).toBeNull();
    expect(parseFrame('')).toBeNull();
    expect(parseFrame(undefined)).toBeNull();
    expect(parseFrame(JSON.stringify({ id: 1, data: {} }))).toBeNull();
    expect(parseFrame(JSON.stringify('just a string'))).toBeNull();
  });

  it('does not touch event type values (only keys are camelized)', () => {
    const ev = parseFrame(
      JSON.stringify({ id: 1, type: 'cluster.k8s_changed', time: 't', data: { kind: 'Pod' } }),
    );
    expect(ev?.type).toBe('cluster.k8s_changed');
  });
});
