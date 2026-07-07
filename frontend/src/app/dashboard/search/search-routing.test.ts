import { searchResultHref, SEARCHABLE_TYPES } from './page';
import { getResourceDef } from '@/lib/k8s-paths';

// F-02 regression: every searchable type must resolve to a concrete in-app
// route when a result row is clicked — no more dead-ending on the generic
// "Resource not found." page.

const CID = 'c1';

describe('searchResultHref', () => {
  it('routes every SEARCHABLE_TYPES value to a non-empty dashboard path', () => {
    for (const { value } of SEARCHABLE_TYPES) {
      const href = searchResultHref(value, CID, 'default', 'thing');
      expect(href.startsWith(`/dashboard/clusters/${CID}/`)).toBe(true);
      // No unresolved template segments and no empty path chunks.
      expect(href).not.toContain('undefined');
    }
  });

  it('sends pods to the workload pod detail', () => {
    expect(searchResultHref('pods', CID, 'kube-system', 'coredns-abc')).toBe(
      '/dashboard/clusters/c1/workloads/pods/kube-system/coredns-abc',
    );
  });

  it('sends workload kinds to their workload detail', () => {
    expect(searchResultHref('deployments', CID, 'app', 'web')).toBe(
      '/dashboard/clusters/c1/workloads/deployment/app/web',
    );
    expect(searchResultHref('cronjobs', CID, 'batch', 'nightly')).toBe(
      '/dashboard/clusters/c1/workloads/cronjob/batch/nightly',
    );
  });

  it('sends nodes to the node detail and namespaces to the list', () => {
    expect(searchResultHref('nodes', CID, '', 'node-1')).toBe(
      '/dashboard/clusters/c1/nodes/node-1',
    );
    expect(searchResultHref('namespaces', CID, '', 'team-a')).toBe(
      '/dashboard/clusters/c1/namespaces',
    );
  });

  it('deep-links generic namespaced objects to the detail route', () => {
    // Services, ConfigMaps, Secrets, Ingresses, PVCs previously fell through
    // to a not-found page; now they land on the generic detail route.
    expect(searchResultHref('services', CID, 'default', 'kubernetes')).toBe(
      '/dashboard/clusters/c1/services/default/kubernetes',
    );
    expect(searchResultHref('secrets', CID, 'default', 'tls-cert')).toBe(
      '/dashboard/clusters/c1/secrets/default/tls-cert',
    );
    expect(searchResultHref('configmaps', CID, 'kube-system', 'coredns')).toBe(
      '/dashboard/clusters/c1/configmaps/kube-system/coredns',
    );
    expect(searchResultHref('persistentvolumeclaims', CID, 'data', 'pvc-1')).toBe(
      '/dashboard/clusters/c1/persistentvolumeclaims/data/pvc-1',
    );
  });

  it('falls back to the resource list when the row carries no name', () => {
    expect(searchResultHref('services', CID, 'default', '')).toBe(
      '/dashboard/clusters/c1/services',
    );
  });

  it('every generic (default-branch) type is a known resource definition', () => {
    // Guards that the detail route the default branch builds is one the
    // [resource]/[...path] page + k8s proxy actually understand.
    const workloadOrSpecial = new Set([
      'pods',
      'deployments',
      'statefulsets',
      'daemonsets',
      'jobs',
      'cronjobs',
      'nodes',
      'namespaces',
    ]);
    for (const { value } of SEARCHABLE_TYPES) {
      if (workloadOrSpecial.has(value)) continue;
      expect(getResourceDef(value)).toBeDefined();
    }
  });
});
