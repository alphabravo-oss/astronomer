import { indexMounts, emptyRegistry, EXTENSION_POINTS } from './registry';
import type { ExtensionMount, ExtensionMountsResponse } from '@/lib/api/extensions';

function mount(over: Partial<ExtensionMount>): ExtensionMount {
  return {
    extension: 'ext',
    displayName: 'Ext',
    point: 'clusterTab',
    pointId: 'Tab',
    tier: 1,
    render: { declarative: { kind: 'table', dataSource: 'd1' } },
    ...over,
  };
}

describe('indexMounts', () => {
  it('returns a fully-populated empty registry for undefined input', () => {
    const reg = indexMounts(undefined);
    for (const p of EXTENSION_POINTS) {
      expect(reg[p]).toEqual([]);
    }
  });

  it('maps the wire buckets (dashboardWidgets/settings) onto canonical point kinds', () => {
    const res: ExtensionMountsResponse = {
      sidebar: [mount({ point: 'sidebar', pointId: 's', render: { declarative: { kind: 'stat', dataSource: 'd' } } })],
      dashboardWidgets: [mount({ point: 'dashboardWidget', pointId: 'w' })],
      clusterTabs: [mount({ point: 'clusterTab', pointId: 't' })],
      settings: [mount({ point: 'settingsPage', pointId: 'p' })],
    };

    const reg = indexMounts(res);

    expect(reg.sidebar).toHaveLength(1);
    expect(reg.dashboardWidget).toHaveLength(1);
    expect(reg.clusterTab).toHaveLength(1);
    expect(reg.settingsPage).toHaveLength(1);
  });

  it('drops a mount with no render (legacy registry entry mounts nothing)', () => {
    const res = {
      ...empty(),
      clusterTabs: [mount({ render: undefined as never })],
    };

    const reg = indexMounts(res);

    expect(reg.clusterTab).toHaveLength(0);
  });

  it('drops a render that has neither declarative nor bundle', () => {
    const res = {
      ...empty(),
      clusterTabs: [mount({ render: {} })],
    };

    expect(indexMounts(res).clusterTab).toHaveLength(0);
  });

  it('keeps a Tier-2 bundle mount', () => {
    const res = {
      ...empty(),
      clusterTabs: [
        mount({
          tier: 2,
          render: {
            bundle: {
              url: 'https://cdn/x.js',
              sha256: 'sha256:' + 'a'.repeat(64),
              integrity: 'sha384-abc',
              entry: 'index.js',
              sandboxOrigin: 'https://ext.sandbox.local',
              component: 'Tab',
            },
          },
        }),
      ],
    };

    expect(indexMounts(res).clusterTab).toHaveLength(1);
  });

  it("files a mount under its own `point` when it disagrees with the bucket it arrived in", () => {
    // A malformed projection puts a clusterTab-pointed mount in the sidebar bucket.
    const res = {
      ...empty(),
      sidebar: [mount({ point: 'clusterTab', pointId: 'smuggled' })],
    };

    const reg = indexMounts(res);

    expect(reg.sidebar).toHaveLength(0);
    expect(reg.clusterTab).toHaveLength(1);
    expect(reg.clusterTab[0].pointId).toBe('smuggled');
  });

  it('falls back to the arrival bucket when `point` is unknown/missing', () => {
    const res = {
      ...empty(),
      clusterTabs: [mount({ point: 'bogus' as never })],
    };

    expect(indexMounts(res).clusterTab).toHaveLength(1);
  });
});

function empty(): ExtensionMountsResponse {
  return { sidebar: [], dashboardWidgets: [], clusterTabs: [], settings: [] };
}

describe('emptyRegistry', () => {
  it('produces independent arrays (no shared reference between buckets)', () => {
    const reg = emptyRegistry();
    reg.sidebar.push(mount({}));
    expect(reg.clusterTab).toHaveLength(0);
  });
});
