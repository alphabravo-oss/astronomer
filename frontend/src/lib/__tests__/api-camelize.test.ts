/**
 * The response interceptor's camelize carve-outs, exercised END TO END.
 *
 * src/lib/api/monitoring-stack.test.ts mocks `@/lib/api` wholesale, so it can
 * only assert what the client asks for — it can never see what the interceptor
 * does to the answer on the way back. That gap is exactly how a corrupted
 * preview shipped: the client's own header claimed the interceptor rewrote
 * nothing but the pagination envelope, while camelizeKeys is recursive and
 * unconditional for every non-carved-out path.
 *
 * These tests drive the REAL instance through a stub axios adapter, so the
 * whole interceptor chain runs.
 */
import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios';
import api, { isMonitoringPreviewPath } from '@/lib/api';

const realAdapter = api.defaults.adapter;

/** Answer every request with `body`, verbatim, so the interceptor is the only actor. */
function respondWith(body: unknown) {
  const adapter: AxiosAdapter = async (config: InternalAxiosRequestConfig) =>
    ({
      data: structuredClone(body),
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }) as AxiosResponse;
  api.defaults.adapter = adapter;
}

afterEach(() => {
  api.defaults.adapter = realAdapter;
});

describe('isMonitoringPreviewPath', () => {
  it('matches all three preview endpoints and nothing adjacent', () => {
    expect(isMonitoringPreviewPath('/clusters/c1/monitoring/stack/preview/')).toBe(true);
    expect(isMonitoringPreviewPath('/settings/monitoring/thanos/preview/')).toBe(true);
    expect(isMonitoringPreviewPath('/settings/monitoring/alertmanager/preview/')).toBe(true);
    expect(isMonitoringPreviewPath('/settings/monitoring/grafana/preview/')).toBe(true);
    expect(isMonitoringPreviewPath('/settings/monitoring/loki/preview/')).toBe(true);

    // Status/install/upgrade/replace/uninstall answer plain camelCase envelopes
    // and MUST keep going through the interceptor.
    expect(isMonitoringPreviewPath('/clusters/c1/monitoring/stack/status/')).toBe(false);
    expect(isMonitoringPreviewPath('/clusters/c1/monitoring/stack/install/')).toBe(false);
    expect(isMonitoringPreviewPath('/settings/monitoring/thanos/upgrade/')).toBe(false);
    expect(isMonitoringPreviewPath('/settings/monitoring/operations/')).toBe(false);
    expect(isMonitoringPreviewPath('/catalog/preview/')).toBe(false);
    expect(isMonitoringPreviewPath(undefined)).toBe(false);
  });
});

describe('monitoring preview responses are not camelized', () => {
  it('keeps shared Alertmanager’s snake_case values.config intact', async () => {
    // Verbatim shape of what sharedAlertmanagerLifecycle renders —
    // internal/handler/monitoring_stack_shared.go:822-868.
    respondWith({
      data: {
        clusterId: 'mgmt-1',
        chart: { repoUrl: 'https://charts.example.io', chartName: 'alertmanager' },
        values: {
          config: {
            global: { resolve_timeout: '5m' },
            route: {
              receiver: 'null',
              group_by: ['alertname', 'astronomer_rule_id', 'cluster'],
              group_wait: '30s',
              group_interval: '5m',
              repeat_interval: '3h',
              routes: [],
            },
            receivers: [
              {
                name: 'channel-1',
                webhook_configs: [{ url: 'https://hooks.example', send_resolved: true }],
                email_configs: [{ to: 'ops@example.io', send_resolved: true }],
              },
            ],
          },
        },
        desiredSpecHash: 'abc123',
        requiresReplace: false,
        replaceReasons: null,
      },
    });

    const res = await api.post('/settings/monitoring/alertmanager/preview/', {});
    const config = res.data.data.values.config;

    // If any of these flipped to camelCase the pane would render YAML the
    // server will never apply, and the spec hash beside it would disagree.
    expect(config.global).toEqual({ resolve_timeout: '5m' });
    expect(config.route.group_by).toEqual(['alertname', 'astronomer_rule_id', 'cluster']);
    expect(config.route.group_wait).toBe('30s');
    expect(config.route.group_interval).toBe('5m');
    expect(config.route.repeat_interval).toBe('3h');
    expect(config.receivers[0].webhook_configs[0].send_resolved).toBe(true);
    expect(config.receivers[0].email_configs[0].send_resolved).toBe(true);
    expect(Object.keys(config.receivers[0])).toEqual([
      'name',
      'webhook_configs',
      'email_configs',
    ]);
  });

  it('keeps the per-cluster externalLabels key, which defaults to the literal cluster_id', async () => {
    // internal/handler/monitoring_stack_cluster.go:416-418 defaults ClusterLabel
    // to "cluster_id"; :450 uses it as the externalLabels MAP KEY.
    respondWith({
      data: {
        clusterId: 'cluster-1',
        chart: { repoUrl: 'https://charts.example.io', chartName: 'kube-prometheus-stack' },
        values: {
          prometheus: {
            prometheusSpec: {
              externalLabels: { cluster_id: 'cluster-1' },
              scrapeInterval: '30s',
            },
          },
        },
        desiredSpecHash: 'def456',
        requiresReplace: false,
        replaceReasons: null,
      },
    });

    const res = await api.post('/clusters/cluster-1/monitoring/stack/preview/', {});
    const spec = res.data.data.values.prometheus.prometheusSpec;
    expect(spec.externalLabels).toEqual({ cluster_id: 'cluster-1' });
    expect(spec.externalLabels.clusterId).toBeUndefined();
  });
});

describe('everything else still gets camelized', () => {
  it('rewrites the pagination envelope on the operations list', async () => {
    respondWith({
      data: [{ id: 'op-1', status: 'pending' }],
      pagination: { total: 1, has_more: false, next_offset: 0 },
    });

    const res = await api.get('/settings/monitoring/operations/');
    expect(res.data.pagination).toEqual({ total: 1, hasMore: false, nextOffset: 0 });
  });
});
