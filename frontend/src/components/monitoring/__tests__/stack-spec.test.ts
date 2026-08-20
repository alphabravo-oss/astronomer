/**
 * The status → form → request-body round-trip.
 *
 * This is not incidental plumbing. The backend's replace-required checks
 * compare the persisted value against the DECODED request field, so an upgrade
 * that omits a field the operator never touched reads as a change and 409s.
 * Seeding the form from status and posting the whole spec back is what prevents
 * that, and these tests are what keep it true.
 */
import {
  CLUSTER_STACK_FAMILY,
  SHARED_ALERTMANAGER_FAMILY,
  SHARED_GRAFANA_FAMILY,
  SHARED_LOKI_FAMILY,
  SHARED_THANOS_FAMILY,
  SERVER_BLIND_FIELDS,
  buildStackBody,
  fleetGrafanaClusterURL,
  fleetGrafanaOpenURL,
  missingRequiredFields,
  replaceTriggeringChanges,
  seedStackValues,
  stackIsInstalled,
  stackStatusLabel,
} from '@/components/monitoring/stack-spec';
import type { MonitoringStackStatusBase } from '@/lib/api/monitoring-stack';

describe('stackIsInstalled', () => {
  it('treats not_configured and uninstalled as absent, matching the backend', () => {
    expect(stackIsInstalled({ status: 'not_configured' })).toBe(false);
    expect(stackIsInstalled({ status: 'uninstalled' })).toBe(false);
    expect(stackIsInstalled(undefined)).toBe(false);
    expect(stackIsInstalled({ status: 'healthy' })).toBe(true);
    expect(stackIsInstalled({ status: 'installing' })).toBe(true);
    expect(stackIsInstalled({ status: 'drifted' })).toBe(true);
  });

  it('labels the recorded lifecycle statuses in operator language', () => {
    expect(stackStatusLabel('not_configured')).toBe('Not installed');
    expect(stackStatusLabel('drifted')).toBe('Drifted');
    expect(stackStatusLabel(undefined)).toBe('Unknown');
  });
});

describe('seedStackValues', () => {
  it("uses the backend's own defaults when no stack exists", () => {
    const values = seedStackValues(CLUSTER_STACK_FAMILY, { status: 'not_configured' });
    expect(values.namespace).toBe('monitoring');
    expect(values.releaseName).toBe('prometheus');
    expect(values.thanosSidecarEnabled).toBe('true');
  });

  it('leaves the fields no status endpoint reports UNSET rather than guessing', () => {
    // GetStackStatus (internal/handler/monitoring_stack_cluster.go:325-341) emits
    // none of these, so there is nothing to seed from and a default would be a
    // guess the operator then posts back on Upgrade. Empty here means "omit".
    const values = seedStackValues(CLUSTER_STACK_FAMILY, { status: 'healthy' });
    for (const name of SERVER_BLIND_FIELDS) {
      expect(values[name]).toBe('');
    }
  });

  it('replays the recorded desired state so an upgrade posts the full spec back', () => {
    const status: MonitoringStackStatusBase = {
      status: 'healthy',
      namespace: 'observability',
      releaseName: 'kps',
      // NOTE: no chartVersion — the per-cluster status handler does not return
      // one. Asserting it here would test a response shape the server cannot
      // produce.
      // Booleans and numbers arrive typed and must survive the string form.
      thanosSidecarEnabled: false,
    } as MonitoringStackStatusBase;

    const values = seedStackValues(CLUSTER_STACK_FAMILY, status);
    expect(values.namespace).toBe('observability');
    expect(values.releaseName).toBe('kps');
    expect(values.chartVersion).toBe('');
    expect(values.thanosSidecarEnabled).toBe('false');
    // Fields the status omits still fall back to the backend default rather
    // than to empty — an empty namespace would be a "namespace change".
    expect(values.retention).toBe('15d');
  });

  it('does seed chartVersion for the shared families, which DO project it', () => {
    // internal/handler/monitoring_stack_shared.go:371 and :424.
    const values = seedStackValues(SHARED_THANOS_FAMILY, {
      status: 'healthy',
      chartVersion: '1.24.0',
    } as MonitoringStackStatusBase);
    expect(values.chartVersion).toBe('1.24.0');
  });

  it('exposes fleet Grafana URL only when authMode is proxy', () => {
    expect(
      fleetGrafanaOpenURL({
        status: 'healthy',
        authMode: 'clusterip',
        grafanaHost: 'grafana.example.com',
      }),
    ).toBeNull();
    expect(
      fleetGrafanaOpenURL({
        status: 'healthy',
        authMode: 'proxy',
        grafanaHost: 'grafana.example.com',
      }),
    ).toBe('https://grafana.example.com/');
    expect(
      fleetGrafanaOpenURL({
        status: 'not_configured',
        authMode: 'proxy',
        grafanaHost: 'grafana.example.com',
      }),
    ).toBeNull();
    expect(
      fleetGrafanaClusterURL(
        {
          status: 'healthy',
          authMode: 'proxy',
          grafanaHost: 'grafana.example.com',
        },
        'cluster-1',
      ),
    ).toBe('https://grafana.example.com/?var-cluster=cluster-1');
    expect(
      fleetGrafanaClusterURL(
        { status: 'healthy', authMode: 'clusterip', grafanaHost: 'grafana.example.com' },
        'cluster-1',
      ),
    ).toBeNull();
  });

  it('seeds Loki chartVersion, skipDiskCheck and ingestHostname from status', () => {
    const values = seedStackValues(SHARED_LOKI_FAMILY, {
      status: 'healthy',
      chartVersion: '6.27.0',
      skipDiskCheck: true,
      ingestHostname: 'loki-ingest.example.com',
      ingestPublic: false,
    } as MonitoringStackStatusBase);
    expect(values.chartVersion).toBe('6.27.0');
    expect(values.skipDiskCheck).toBe('true');
    expect(values.ingestHostname).toBe('loki-ingest.example.com');
  });

  it('seeds Grafana chartVersion and autoRollbackOnFailure from status (not SERVER_BLIND)', () => {
    const values = seedStackValues(SHARED_GRAFANA_FAMILY, {
      status: 'healthy',
      chartVersion: '8.12.1',
      autoRollbackOnFailure: true,
      authMode: 'clusterip',
    } as MonitoringStackStatusBase);
    expect(values.chartVersion).toBe('8.12.1');
    expect(values.autoRollbackOnFailure).toBe('true');
  });

  it('ignores a stale recorded spec once the stack has been uninstalled', () => {
    const values = seedStackValues(SHARED_THANOS_FAMILY, {
      status: 'uninstalled',
      namespace: 'gone',
    } as MonitoringStackStatusBase);
    expect(values.namespace).toBe('monitoring');
  });

  it('coerces numeric status fields for the shared families', () => {
    const values = seedStackValues(SHARED_THANOS_FAMILY, {
      status: 'healthy',
      queryReplicas: 4,
    } as MonitoringStackStatusBase);
    expect(values.queryReplicas).toBe('4');
  });
});

describe('buildStackBody', () => {
  it('emits camelCase keys — this surface is the repo exception, not snake_case', () => {
    const body = buildStackBody(SHARED_THANOS_FAMILY, {
      ...seedStackValues(SHARED_THANOS_FAMILY, null),
      managementClusterId: 'c-1',
      storageConfigId: 's-1',
    }) as Record<string, unknown>;

    expect(body).toMatchObject({
      managementClusterId: 'c-1',
      storageConfigId: 's-1',
      releaseName: 'thanos',
    });
    expect(Object.keys(body)).not.toContain('management_cluster_id');
    expect(Object.keys(body)).not.toContain('storage_config_id');
  });

  it('parses numbers and omits blanks so the backend applies its own defaults', () => {
    const body = buildStackBody(SHARED_ALERTMANAGER_FAMILY, {
      managementClusterId: 'c-1',
      namespace: '  ',
      releaseName: 'am',
      chartVersion: '',
      replicas: '3',
      storageClass: '',
      storageSize: '5Gi',
      autoRollbackOnFailure: 'false',
    }) as Record<string, unknown>;

    expect(body.replicas).toBe(3);
    expect(body.storageSize).toBe('5Gi');
    expect(body).not.toHaveProperty('namespace');
    expect(body).not.toHaveProperty('chartVersion');
    expect(body).not.toHaveProperty('storageClass');
  });

  it('always sends a plain boolean, which the status endpoint does round-trip', () => {
    // thanosSidecarEnabled comes back from GetStackStatus, so the form knows
    // the operator's real choice and must post it — an absent one reads as true.
    const seeded = seedStackValues(CLUSTER_STACK_FAMILY, null);
    expect(
      (buildStackBody(CLUSTER_STACK_FAMILY, seeded) as Record<string, unknown>)
        .thanosSidecarEnabled,
    ).toBe(true);
    expect(
      (
        buildStackBody(CLUSTER_STACK_FAMILY, {
          ...seeded,
          thanosSidecarEnabled: 'false',
        }) as Record<string, unknown>
      ).thanosSidecarEnabled,
    ).toBe(false);
  });

  it('OMITS an untouched tri-state, so the backend applies its own policy', () => {
    // The damaging case: resolveAutoRollbackPolicy
    // (internal/handler/monitoring_operations.go:731-734) honours an override
    // BEFORE consulting operationPolicies.defaultAutoRollbackOnFailure, so
    // sending a guessed `true` silently defeats a platform-wide `false`.
    // Likewise enableGrafana / enableAlertmanager must not come back ON.
    const body = buildStackBody(
      CLUSTER_STACK_FAMILY,
      seedStackValues(CLUSTER_STACK_FAMILY, null),
    ) as Record<string, unknown>;

    for (const name of SERVER_BLIND_FIELDS) {
      expect(body).not.toHaveProperty(name);
    }
  });

  it('sends a tri-state the operator actually chose, in either direction', () => {
    const pick = (value: string) =>
      buildStackBody(CLUSTER_STACK_FAMILY, {
        ...seedStackValues(CLUSTER_STACK_FAMILY, null),
        autoRollbackOnFailure: value,
        enableGrafana: value,
      }) as Record<string, unknown>;

    expect(pick('false')).toMatchObject({
      autoRollbackOnFailure: false,
      enableGrafana: false,
    });
    expect(pick('true')).toMatchObject({
      autoRollbackOnFailure: true,
      enableGrafana: true,
    });
  });

  it('omits autoRollbackOnFailure for the shared families too — none project it', () => {
    for (const family of [SHARED_THANOS_FAMILY, SHARED_ALERTMANAGER_FAMILY]) {
      const body = buildStackBody(family, {
        ...seedStackValues(family, { status: 'healthy' } as MonitoringStackStatusBase),
        managementClusterId: 'c-1',
        storageConfigId: 's-1',
      }) as Record<string, unknown>;
      expect(body).not.toHaveProperty('autoRollbackOnFailure');
    }
  });
});

describe('missingRequiredFields', () => {
  it('names the two fields the shared Thanos handler rejects the request without', () => {
    const missing = missingRequiredFields(
      SHARED_THANOS_FAMILY,
      seedStackValues(SHARED_THANOS_FAMILY, null),
    ).map((field) => field.name);
    expect(missing).toEqual(['managementClusterId', 'storageConfigId']);
  });

  it('reports nothing for the per-cluster stack, whose fields are all optional', () => {
    expect(
      missingRequiredFields(CLUSTER_STACK_FAMILY, seedStackValues(CLUSTER_STACK_FAMILY, null)),
    ).toEqual([]);
  });
});

describe('replaceTriggeringChanges', () => {
  const installed = {
    status: 'healthy',
    namespace: 'monitoring',
    releaseName: 'prometheus',
    storageClass: 'default',
  } as MonitoringStackStatusBase;

  it('flags an edit that cannot be an in-place upgrade before the request is sent', () => {
    const values = { ...seedStackValues(CLUSTER_STACK_FAMILY, installed), namespace: 'observability' };
    expect(replaceTriggeringChanges(CLUSTER_STACK_FAMILY, values, installed)).toEqual(['Namespace']);
  });

  it('stays quiet for an upgrade-safe edit', () => {
    const values = { ...seedStackValues(CLUSTER_STACK_FAMILY, installed), retention: '30d' };
    expect(replaceTriggeringChanges(CLUSTER_STACK_FAMILY, values, installed)).toEqual([]);
  });

  it('says nothing at all when there is no release yet — everything is an install', () => {
    const values = { ...seedStackValues(CLUSTER_STACK_FAMILY, null), namespace: 'anywhere' };
    expect(replaceTriggeringChanges(CLUSTER_STACK_FAMILY, values, { status: 'not_configured' })).toEqual(
      [],
    );
  });
});
