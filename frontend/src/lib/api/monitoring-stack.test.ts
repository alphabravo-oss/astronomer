import type { Mocked } from 'vitest';
import api from '@/lib/api';
import {
  getClusterStackStatus,
  previewClusterStack,
  installClusterStack,
  upgradeClusterStack,
  replaceClusterStack,
  uninstallClusterStack,
  getSharedThanosStatus,
  previewSharedThanos,
  installSharedThanos,
  upgradeSharedThanos,
  replaceSharedThanos,
  uninstallSharedThanos,
  getSharedAlertmanagerStatus,
  previewSharedAlertmanager,
  installSharedAlertmanager,
  upgradeSharedAlertmanager,
  replaceSharedAlertmanager,
  uninstallSharedAlertmanager,
  getSharedGrafanaStatus,
  previewSharedGrafana,
  installSharedGrafana,
  upgradeSharedGrafana,
  replaceSharedGrafana,
  uninstallSharedGrafana,
  listMonitoringOperations,
  getMonitoringOperation,
  retryMonitoringOperation,
  runStackLifecycle,
  operationTargetOf,
  parseReplaceRequiredError,
  isActiveOperationStatus,
  isTerminalOperationStatus,
  isRetryableOperationStatus,
  type SharedThanosRequest,
} from './monitoring-stack';

vi.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const mockedApi = api as Mocked<typeof api>;

const OP = {
  id: 'op-1',
  targetType: 'cluster_stack',
  targetKey: 'cluster-1',
  operationType: 'install',
  status: 'pending',
  attemptCount: 0,
  errorMessage: '',
  createdAt: '2026-07-29T10:00:00Z',
  updatedAt: '2026-07-29T10:00:00Z',
};

const THANOS_BODY: SharedThanosRequest = {
  managementClusterId: 'mgmt-1',
  storageConfigId: 'store-1',
  queryReplicas: 3,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockedApi.get.mockResolvedValue({ data: { data: {} } } as never);
  mockedApi.post.mockResolvedValue({ data: { data: OP } } as never);
  mockedApi.put.mockResolvedValue({ data: { data: OP } } as never);
  mockedApi.delete.mockResolvedValue({ data: { data: OP } } as never);
});

describe('per-cluster monitoring stack endpoints', () => {
  it('GET status hits the cluster-scoped route and unwraps the envelope', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: { data: { status: 'healthy', releaseName: 'prometheus' } },
    } as never);
    await expect(getClusterStackStatus('cluster-1')).resolves.toEqual({
      status: 'healthy',
      releaseName: 'prometheus',
    });
    expect(mockedApi.get).toHaveBeenCalledWith('/clusters/cluster-1/monitoring/stack/status/');
  });

  it('POST preview sends the body and unwraps the rendered values', async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: {
        data: {
          clusterId: 'cluster-1',
          chart: { repoUrl: 'https://example', chartName: 'kube-prometheus-stack' },
          values: { grafana: { enabled: true } },
          desiredSpecHash: 'abc',
          requiresReplace: true,
          replaceReasons: ['namespace change'],
        },
      },
    } as never);
    const preview = await previewClusterStack('cluster-1', { namespace: 'obs' });
    expect(preview.requiresReplace).toBe(true);
    expect(preview.replaceReasons).toEqual(['namespace change']);
    expect(mockedApi.post).toHaveBeenCalledWith('/clusters/cluster-1/monitoring/stack/preview/', {
      namespace: 'obs',
    });
  });

  it('install/upgrade/replace use POST/PUT/POST and return the enqueued operation', async () => {
    await expect(installClusterStack('cluster-1', { retention: '30d' })).resolves.toEqual(OP);
    expect(mockedApi.post).toHaveBeenCalledWith('/clusters/cluster-1/monitoring/stack/install/', {
      retention: '30d',
    });

    await upgradeClusterStack('cluster-1', { retention: '30d' });
    expect(mockedApi.put).toHaveBeenCalledWith('/clusters/cluster-1/monitoring/stack/upgrade/', {
      retention: '30d',
    });

    await replaceClusterStack('cluster-1', {});
    expect(mockedApi.post).toHaveBeenCalledWith('/clusters/cluster-1/monitoring/stack/replace/', {});
  });

  it('DELETE uninstall sends no body — the release comes from the stored config', async () => {
    await expect(uninstallClusterStack('cluster-1')).resolves.toEqual(OP);
    expect(mockedApi.delete).toHaveBeenCalledWith('/clusters/cluster-1/monitoring/stack/uninstall/');
  });
});

describe('shared Thanos endpoints', () => {
  it('covers status, preview and the three write verbs under /settings/monitoring/thanos/', async () => {
    await getSharedThanosStatus();
    expect(mockedApi.get).toHaveBeenCalledWith('/settings/monitoring/thanos/status/');

    await previewSharedThanos(THANOS_BODY);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/thanos/preview/', THANOS_BODY);

    await installSharedThanos(THANOS_BODY);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/thanos/install/', THANOS_BODY);

    await upgradeSharedThanos(THANOS_BODY);
    expect(mockedApi.put).toHaveBeenCalledWith('/settings/monitoring/thanos/upgrade/', THANOS_BODY);

    await replaceSharedThanos(THANOS_BODY);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/thanos/replace/', THANOS_BODY);
  });

  it('DELETE uninstall passes clusterId as a query param, and omits it when unknown', async () => {
    await uninstallSharedThanos('mgmt-1');
    expect(mockedApi.delete).toHaveBeenCalledWith('/settings/monitoring/thanos/uninstall/', {
      params: { clusterId: 'mgmt-1' },
    });

    await uninstallSharedThanos();
    expect(mockedApi.delete).toHaveBeenLastCalledWith(
      '/settings/monitoring/thanos/uninstall/',
      undefined,
    );
  });
});

describe('shared Grafana endpoints', () => {
  it('covers status, preview and the three write verbs under /settings/monitoring/grafana/', async () => {
    const body = { managementClusterId: 'mgmt-1', replicas: 1 };
    await getSharedGrafanaStatus();
    expect(mockedApi.get).toHaveBeenCalledWith('/settings/monitoring/grafana/status/');

    await previewSharedGrafana(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/grafana/preview/', body);

    await installSharedGrafana(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/grafana/install/', body);

    await upgradeSharedGrafana(body);
    expect(mockedApi.put).toHaveBeenCalledWith('/settings/monitoring/grafana/upgrade/', body);

    await replaceSharedGrafana(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/grafana/replace/', body);

    await uninstallSharedGrafana('mgmt-1');
    expect(mockedApi.delete).toHaveBeenCalledWith('/settings/monitoring/grafana/uninstall/', {
      params: { clusterId: 'mgmt-1' },
    });
  });
});

describe('shared Alertmanager endpoints', () => {
  it('covers status, preview and the three write verbs under /settings/monitoring/alertmanager/', async () => {
    const body = { managementClusterId: 'mgmt-1', replicas: 2 };
    await getSharedAlertmanagerStatus();
    expect(mockedApi.get).toHaveBeenCalledWith('/settings/monitoring/alertmanager/status/');

    await previewSharedAlertmanager(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/alertmanager/preview/', body);

    await installSharedAlertmanager(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/alertmanager/install/', body);

    await upgradeSharedAlertmanager(body);
    expect(mockedApi.put).toHaveBeenCalledWith('/settings/monitoring/alertmanager/upgrade/', body);

    await replaceSharedAlertmanager(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/alertmanager/replace/', body);

    await uninstallSharedAlertmanager('mgmt-1');
    expect(mockedApi.delete).toHaveBeenCalledWith('/settings/monitoring/alertmanager/uninstall/', {
      params: { clusterId: 'mgmt-1' },
    });
  });
});

describe('request bodies keep the handlers camelCase spelling', () => {
  // The repo default is snake_case request bodies; these handlers are the
  // exception (camelCase json tags). Re-spelling them snake_case would decode
  // to zero values server-side, which is silent.
  it('passes camelCase keys straight through', async () => {
    await installSharedThanos({
      managementClusterId: 'mgmt-1',
      storageConfigId: 'store-1',
      objectStorageSecretName: 'thanos-objstore',
      storeGatewayReplicas: 2,
      autoRollbackOnFailure: true,
    });
    const [, body] = mockedApi.post.mock.calls.at(-1)!;
    expect(Object.keys(body as object)).toEqual([
      'managementClusterId',
      'storageConfigId',
      'objectStorageSecretName',
      'storeGatewayReplicas',
      'autoRollbackOnFailure',
    ]);
  });
});

describe('operations queue endpoints', () => {
  it('GET list forwards the camelCase target filters and tolerates a bare array', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: [OP], pagination: { total: 1 } } } as never);
    await expect(
      listMonitoringOperations({ targetType: 'shared_thanos', targetKey: 'shared', limit: 5 }),
    ).resolves.toEqual([OP]);
    expect(mockedApi.get).toHaveBeenCalledWith('/settings/monitoring/operations/', {
      params: { targetType: 'shared_thanos', targetKey: 'shared', limit: 5 },
    });

    mockedApi.get.mockResolvedValueOnce({ data: { data: null } } as never);
    await expect(listMonitoringOperations()).resolves.toEqual([]);
  });

  it('GET detail and POST retry address the operation by id', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: { ...OP, events: [] } } } as never);
    await getMonitoringOperation('op-1');
    expect(mockedApi.get).toHaveBeenCalledWith('/settings/monitoring/operations/op-1/');

    mockedApi.post.mockResolvedValueOnce({ data: { data: { ...OP, status: 'pending' } } } as never);
    await expect(retryMonitoringOperation('op-1')).resolves.toEqual(
      expect.objectContaining({ id: 'op-1', status: 'pending' }),
    );
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/operations/op-1/retry/');
  });
});

describe('target dispatch', () => {
  it('maps each target to the (targetType, targetKey) the queue records', () => {
    expect(operationTargetOf({ kind: 'cluster', clusterId: 'c1' })).toEqual({
      targetType: 'cluster_stack',
      targetKey: 'c1',
    });
    expect(operationTargetOf({ kind: 'thanos' })).toEqual({
      targetType: 'shared_thanos',
      targetKey: 'shared',
    });
    expect(operationTargetOf({ kind: 'alertmanager' })).toEqual({
      targetType: 'shared_alertmanager',
      targetKey: 'shared',
    });
    expect(operationTargetOf({ kind: 'grafana' })).toEqual({
      targetType: 'shared_grafana',
      targetKey: 'shared',
    });
  });

  it('routes every verb of every family to the right endpoint', async () => {
    await runStackLifecycle({ kind: 'cluster', clusterId: 'c1' }, 'uninstall', { namespace: 'x' });
    expect(mockedApi.delete).toHaveBeenCalledWith('/clusters/c1/monitoring/stack/uninstall/');

    await runStackLifecycle({ kind: 'thanos' }, 'upgrade', THANOS_BODY);
    expect(mockedApi.put).toHaveBeenCalledWith('/settings/monitoring/thanos/upgrade/', THANOS_BODY);

    await runStackLifecycle({ kind: 'alertmanager' }, 'uninstall', {
      managementClusterId: 'mgmt-1',
    });
    expect(mockedApi.delete).toHaveBeenLastCalledWith(
      '/settings/monitoring/alertmanager/uninstall/',
      { params: { clusterId: 'mgmt-1' } },
    );

    await runStackLifecycle({ kind: 'grafana' }, 'install', {
      managementClusterId: 'mgmt-1',
    });
    expect(mockedApi.post).toHaveBeenCalledWith('/settings/monitoring/grafana/install/', {
      managementClusterId: 'mgmt-1',
    });
  });
});

describe('replace_required conflicts', () => {
  it('reads the 409 body that RespondJSON wraps, which the generic error helper cannot', () => {
    const err = {
      response: {
        status: 409,
        data: {
          data: {
            error: 'replace_required',
            message: 'Requested Thanos changes require reinstall rather than in-place upgrade',
            requiresReplace: true,
            replaceReasons: ['namespace change', 'object storage configuration change'],
          },
        },
      },
    };
    expect(parseReplaceRequiredError(err)).toEqual({
      message: 'Requested Thanos changes require reinstall rather than in-place upgrade',
      replaceReasons: ['namespace change', 'object storage configuration change'],
    });
  });

  it('returns null for any other failure so the caller falls back to the generic toast', () => {
    expect(parseReplaceRequiredError(new Error('boom'))).toBeNull();
    expect(
      parseReplaceRequiredError({ response: { status: 500, data: { error: { code: 'x' } } } }),
    ).toBeNull();
    expect(
      parseReplaceRequiredError({ response: { status: 409, data: { data: { error: 'other' } } } }),
    ).toBeNull();
  });
});

describe('operation status predicates mirror internal/operationstate', () => {
  it('classifies all five states', () => {
    expect(['pending', 'running'].every(isActiveOperationStatus)).toBe(true);
    expect(['completed', 'failed', 'superseded'].every(isTerminalOperationStatus)).toBe(true);
    expect(['failed', 'superseded'].every(isRetryableOperationStatus)).toBe(true);
    expect(isRetryableOperationStatus('completed')).toBe(false);
    expect(isRetryableOperationStatus('running')).toBe(false);
    expect(isActiveOperationStatus(undefined)).toBe(false);
  });
});
