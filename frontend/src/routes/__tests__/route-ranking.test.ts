/**
 * D4 route-ranking proof (P2.2): TanStack Router must rank static > dynamic >
 * splat so the ~19 static siblings under clusters/$id win over `$resource`,
 * and `$resource` wins over its own `$` splat. Written against the real
 * generated route tree BEFORE the mass port (P2.3) — if any row here fails,
 * restructure with explicit route ordering before porting further.
 */
import { describe, expect, it } from 'vitest';
import { createMemoryHistory, createRouter } from '@tanstack/react-router';
import { routeTree } from '@/routeTree.gen';
import { hrefToLocation } from '@/lib/link';

const router = createRouter({
  routeTree,
  history: createMemoryHistory({ initialEntries: ['/'] }),
});

function leafRouteId(pathname: string): string {
  const matches = router.matchRoutes(pathname);
  expect(matches.length).toBeGreaterThan(0);
  return matches[matches.length - 1].routeId as string;
}

// [URL, expected leaf route id]
const rows: Array<[string, string]> = [
  // ── Static siblings under clusters/$id beat the $resource dynamic segment ──
  ['/dashboard/clusters/c1/adoption', '/dashboard/clusters/$id/adoption/'],
  ['/dashboard/clusters/c1/apps', '/dashboard/clusters/$id/apps/'],
  ['/dashboard/clusters/c1/gatekeeper', '/dashboard/clusters/$id/gatekeeper/'],
  ['/dashboard/clusters/c1/image-scans', '/dashboard/clusters/$id/image-scans/'],
  ['/dashboard/clusters/c1/network-access', '/dashboard/clusters/$id/network-access/'],
  ['/dashboard/clusters/c1/network-policies', '/dashboard/clusters/$id/network-policies/'],
  ['/dashboard/clusters/c1/registries', '/dashboard/clusters/$id/registries/'],
  ['/dashboard/clusters/c1/resources', '/dashboard/clusters/$id/resources/'],
  ['/dashboard/clusters/c1/service-mesh', '/dashboard/clusters/$id/service-mesh/'],
  ['/dashboard/clusters/c1/service-mesh/mtls', '/dashboard/clusters/$id/service-mesh/mtls/'],
  ['/dashboard/clusters/c1/shell', '/dashboard/clusters/$id/shell/'],
  ['/dashboard/clusters/c1/snapshots', '/dashboard/clusters/$id/snapshots/'],
  ['/dashboard/clusters/c1/template', '/dashboard/clusters/$id/template/'],
  ['/dashboard/clusters/c1/tools', '/dashboard/clusters/$id/tools/'],
  ['/dashboard/clusters/c1/workloads', '/dashboard/clusters/$id/workloads/'],
  // Static prefix + own dynamic children still beat $resource/$.
  ['/dashboard/clusters/c1/nodes/node-1', '/dashboard/clusters/$id/nodes/$nodeName/'],
  [
    '/dashboard/clusters/c1/workloads/Deployment/default/web',
    '/dashboard/clusters/$id/workloads/$kind/$namespace/$name/',
  ],

  // ── The $resource dynamic segment and its splat child ──
  ['/dashboard/clusters/c1', '/dashboard/clusters/$id/'],
  ['/dashboard/clusters/c1/deployments', '/dashboard/clusters/$id/$resource/'],
  ['/dashboard/clusters/c1/deployments/ns/foo', '/dashboard/clusters/$id/$resource/$'],
  ['/dashboard/clusters/c1/pods/kube-system/coredns-abc', '/dashboard/clusters/$id/$resource/$'],

  // ── The [[...slug]] optional-catch-all split: paired index + splat files ──
  ['/dashboard/clusters/c1/custom-resources', '/dashboard/clusters/$id/custom-resources/'],
  [
    '/dashboard/clusters/c1/custom-resources/g/v1/things',
    '/dashboard/clusters/$id/custom-resources/$',
  ],

  // ── The snapshots alias keeps its own route ──
  [
    '/dashboard/clusters/c1/control-plane-snapshots',
    '/dashboard/clusters/$id/control-plane-snapshots/',
  ],

  // ── register (static) beats clusters/$id (dynamic) ──
  ['/dashboard/clusters/register', '/dashboard/clusters/register/'],
  ['/dashboard/clusters/register/abc/connect', '/dashboard/clusters/register/$id/connect/'],
  ['/dashboard/clusters/register/abc/progress', '/dashboard/clusters/register/$id/progress/'],
];

describe('route ranking (D4)', () => {
  it.each(rows)('%s resolves to %s', (url, routeId) => {
    expect(leafRouteId(url)).toBe(routeId);
  });

  it('the clusters/$id layout participates in every subtree match', () => {
    const matches = router.matchRoutes('/dashboard/clusters/c1/deployments');
    expect(matches.map((m) => m.routeId)).toContain('/dashboard/clusters/$id');
  });

  it('splat params surface as _splat', () => {
    const matches = router.matchRoutes('/dashboard/clusters/c1/deployments/ns/foo');
    const leaf = matches[matches.length - 1];
    expect(leaf.params).toMatchObject({ id: 'c1', resource: 'deployments', _splat: 'ns/foo' });
  });
});

describe('hrefToLocation', () => {
  it('parses query-string hrefs into a search object', () => {
    expect(hrefToLocation('/dashboard/audit?actor=alice&action=login')).toEqual({
      to: '/dashboard/audit',
      search: { actor: 'alice', action: 'login' },
    });
  });
});
