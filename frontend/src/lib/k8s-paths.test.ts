import { resolveDetailSlug, detailHref } from './k8s-paths';

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
