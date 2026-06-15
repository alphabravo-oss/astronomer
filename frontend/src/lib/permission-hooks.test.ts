import { canonicalPermissionResource } from './permission-hooks';

// GATE-B perm-gate fix: the generic ResourceDetail must gate on the SAME
// canonical permission resource the list rows use, not the generic 'clusters'
// verb. These cases mirror the resource-list page's mapping.
describe('canonicalPermissionResource', () => {
  it.each([
    ['pods', 'pods'],
    ['services', 'services'],
    ['endpoints', 'services'],
    ['ingresses', 'ingresses'],
    ['gateways', 'ingresses'],
    ['networkpolicies', 'network_policies'],
    ['persistentvolumeclaims', 'storage'],
    ['storageclasses', 'storage'],
    ['configmaps', 'configmaps'],
    ['secrets', 'secrets'],
    ['nodes', 'nodes'],
    ['deployments', 'workloads'],
    ['statefulsets', 'workloads'],
    ['hpa', 'workloads'],
  ])('maps %s -> %s', (resourceType, expected) => {
    expect(canonicalPermissionResource(resourceType)).toBe(expected);
  });

  it('falls back to clusters for unknown / custom kinds', () => {
    expect(canonicalPermissionResource('customthings')).toBe('clusters');
  });

  it('is case-insensitive', () => {
    expect(canonicalPermissionResource('Pods')).toBe('pods');
  });
});
