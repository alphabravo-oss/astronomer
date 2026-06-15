import {
  resolveDetailSlug,
  detailHref,
  crListPath,
  crResourcePath,
  crListHref,
  crDetailHref,
} from './k8s-paths';

describe('resolveDetailSlug', () => {
  it('resolves namespaced kinds to [namespace, name]', () => {
    expect(resolveDetailSlug('pods', ['default', 'my-pod'])).toEqual({
      namespace: 'default',
      name: 'my-pod',
    });
  });

  it('resolves cluster-scoped kinds to [name] with no namespace', () => {
    expect(resolveDetailSlug('nodes', ['node-1'])).toEqual({
      namespace: undefined,
      name: 'node-1',
    });
  });
});

describe('detailHref', () => {
  it('includes the namespace segment for namespaced kinds', () => {
    expect(detailHref('c1', 'services', 'kube-system', 'kube-dns')).toBe(
      '/dashboard/clusters/c1/services/kube-system/kube-dns'
    );
  });

  it('omits the namespace segment for cluster-scoped kinds', () => {
    expect(detailHref('c1', 'nodes', undefined, 'node-1')).toBe(
      '/dashboard/clusters/c1/nodes/node-1'
    );
  });
});

// ── GATE C: custom-resource (CRD instance) helpers ──

describe('crListPath / crResourcePath', () => {
  it('builds an apis list path for a grouped CRD', () => {
    expect(crListPath('example.com', 'v1', 'widgets')).toBe('apis/example.com/v1/widgets');
  });

  it('builds a namespaced single-resource path', () => {
    expect(crResourcePath('example.com', 'v1', 'widgets', 'w1', 'team-a')).toBe(
      'apis/example.com/v1/namespaces/team-a/widgets/w1'
    );
  });

  it('builds a cluster-scoped single-resource path (no namespace)', () => {
    expect(crResourcePath('example.com', 'v1', 'widgets', 'w1')).toBe(
      'apis/example.com/v1/widgets/w1'
    );
  });

  it('falls back to a core api path when group is empty', () => {
    expect(crListPath('', 'v1', 'things')).toBe('api/v1/things');
  });
});

describe('crListHref / crDetailHref', () => {
  it('uses the _ sentinel for an empty group segment', () => {
    expect(crListHref('c1', '', 'v1', 'things')).toBe(
      '/dashboard/clusters/c1/custom-resources/_/v1/things'
    );
  });

  it('appends ns/name for namespaced CR detail', () => {
    expect(crDetailHref('c1', 'example.com', 'v1', 'widgets', 'w1', 'team-a')).toBe(
      '/dashboard/clusters/c1/custom-resources/example.com/v1/widgets/team-a/w1'
    );
  });

  it('appends only name for cluster-scoped CR detail', () => {
    expect(crDetailHref('c1', 'example.com', 'v1', 'widgets', 'w1')).toBe(
      '/dashboard/clusters/c1/custom-resources/example.com/v1/widgets/w1'
    );
  });
});
