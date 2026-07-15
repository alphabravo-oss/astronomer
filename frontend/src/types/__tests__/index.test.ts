/**
 * Tests for src/types/index.ts
 *
 * Verifies that type definitions compile correctly by creating objects that
 * satisfy the type interfaces. These tests primarily validate that the types
 * are structurally sound and can be used at runtime.
 */

import type {
  APIResponse,
  PaginatedResponse,
  APIError,
  Cluster,
  ClusterStatus,
  ClusterProvider,
  ClusterEnvironment,
  ClusterHealthComponent,
  ClusterNode,
  Workload,
  WorkloadKind,
  Pod,
  Container,
  User,
  PolicyRule,
  RoleBinding,
  ArgoInstance,
  ArgoApplication,
  TimeSeriesPoint,
  MetricsSummary,
  SSOProvider,
  APIToken,
  AuditLogEntry,
  ActivityEvent,
} from '@/types/index';

// ---------------------------------------------------------------------------
// API response types
// ---------------------------------------------------------------------------

describe('API Response types', () => {
  it('APIResponse can be created', () => {
    const response: APIResponse<string> = {
      data: 'hello',
      status: 200,
    };
    expect(response.data).toBe('hello');
    expect(response.status).toBe(200);
  });

  it('APIResponse with optional message', () => {
    const response: APIResponse<number> = {
      data: 42,
      status: 200,
      message: 'Success',
    };
    expect(response.message).toBe('Success');
  });

  it('PaginatedResponse can be created', () => {
    const response: PaginatedResponse<string> = {
      data: ['a', 'b'],
      total: 10,
      page: 1,
      pageSize: 2,
      totalPages: 5,
    };
    expect(response.data).toHaveLength(2);
    expect(response.totalPages).toBe(5);
  });

  it('APIError can be created', () => {
    const error: APIError = {
      message: 'Not found',
      code: 'NOT_FOUND',
      status: 404,
    };
    expect(error.status).toBe(404);
  });
});

// ---------------------------------------------------------------------------
// Cluster types
// ---------------------------------------------------------------------------

describe('Cluster types', () => {
  it('Cluster object satisfies interface', () => {
    const cluster: Cluster = {
      id: 'c-1',
      name: 'prod-us-east',
      displayName: 'Production US East',
      status: 'active',
      health: {
        status: 'active',
        lastCheck: '2024-01-01T00:00:00Z',
        components: [],
      },
      provider: 'aws',
      distribution: 'eks',
      environment: 'production',
      region: 'us-east-1',
      kubernetesVersion: '1.28.3',
      nodeCount: 5,
      podCount: 100,
      namespaceCount: 10,
      cpuCapacity: 20000,
      cpuUsage: 8000,
      cpuPercentage: 40,
      memoryCapacity: 64000000000,
      memoryUsage: 32000000000,
      memoryPercentage: 50,
      labels: { env: 'prod' },
      annotations: {},
      agentVersion: '0.1.0',
      lastHeartbeat: '2024-01-01T00:00:00Z',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
    };
    expect(cluster.id).toBe('c-1');
    expect(cluster.provider).toBe('aws');
  });

  it('ClusterStatus accepts all valid values', () => {
    const statuses: ClusterStatus[] = [
      'active', 'connecting', 'warning', 'error', 'disconnected', 'provisioning',
    ];
    expect(statuses).toHaveLength(6);
  });

  it('ClusterProvider accepts all valid values', () => {
    const providers: ClusterProvider[] = [
      'aws', 'gcp', 'azure', 'on-prem', 'digitalocean', 'other',
    ];
    expect(providers).toHaveLength(6);
  });

  it('ClusterEnvironment accepts all valid values', () => {
    const envs: ClusterEnvironment[] = [
      'production', 'staging', 'development', 'testing',
    ];
    expect(envs).toHaveLength(4);
  });

  it('ClusterHealthComponent can be created', () => {
    const component: ClusterHealthComponent = {
      name: 'api-server',
      status: 'healthy',
    };
    expect(component.name).toBe('api-server');
  });

  it('ClusterNode can be created', () => {
    const node: ClusterNode = {
      name: 'node-1',
      status: 'Ready',
      roles: ['master'],
      kubernetesVersion: '1.28.3',
      os: 'linux',
      architecture: 'amd64',
      containerRuntime: 'containerd://1.7.0',
      cpuCapacity: 4000,
      cpuUsage: 1500,
      memoryCapacity: 16000000000,
      memoryUsage: 8000000000,
      podCapacity: 110,
      podCount: 42,
      conditions: [],
      createdAt: '2024-01-01T00:00:00Z',
    };
    expect(node.name).toBe('node-1');
    expect(node.status).toBe('Ready');
  });
});

// ---------------------------------------------------------------------------
// Workload types
// ---------------------------------------------------------------------------

describe('Workload types', () => {
  it('Workload object satisfies interface', () => {
    const workload: Workload = {
      name: 'nginx',
      namespace: 'default',
      kind: 'Deployment',
      clusterId: 'c-1',
      clusterName: 'prod',
      status: 'Running',
      ready: '3/3',
      upToDate: 3,
      available: 3,
      replicas: 3,
      desiredReplicas: 3,
      images: ['nginx:latest'],
      labels: {},
      annotations: {},
      createdAt: '2024-01-01T00:00:00Z',
      age: '30d',
    };
    expect(workload.kind).toBe('Deployment');
  });

  it('WorkloadKind accepts all valid values', () => {
    const kinds: WorkloadKind[] = [
      'Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'ReplicaSet',
    ];
    expect(kinds).toHaveLength(6);
  });

  it('Pod object satisfies interface', () => {
    const pod: Pod = {
      name: 'nginx-abc123',
      namespace: 'default',
      clusterId: 'c-1',
      phase: 'Running',
      status: 'Running',
      ready: '1/1',
      restarts: 0,
      node: 'node-1',
      ip: '10.0.0.5',
      containers: [],
      conditions: [],
      createdAt: '2024-01-01T00:00:00Z',
      age: '5d',
    };
    expect(pod.phase).toBe('Running');
  });

  it('Container can be created', () => {
    const container: Container = {
      name: 'nginx',
      image: 'nginx:latest',
      status: 'running',
      ready: true,
      restartCount: 0,
    };
    expect(container.ready).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// RBAC types
// ---------------------------------------------------------------------------

describe('RBAC types', () => {
  it('User object satisfies interface', () => {
    const user: User = {
      id: 'u-1',
      username: 'admin',
      email: 'admin@example.com',
      displayName: 'Admin User',
      provider: 'local',
      globalRoles: ['admin'],
      enabled: true,
      lastLogin: '2024-01-01T00:00:00Z',
      createdAt: '2024-01-01T00:00:00Z',
    };
    expect(user.username).toBe('admin');
  });

  it('PolicyRule can be created', () => {
    const rule: PolicyRule = {
      apiGroups: [''],
      resources: ['pods'],
      verbs: ['get', 'list', 'watch'],
    };
    expect(rule.verbs).toContain('get');
  });

  it('RoleBinding can be created', () => {
    const binding: RoleBinding = {
      id: 'rb-1',
      name: 'admin-binding',
      roleType: 'global',
      roleName: 'admin',
      subjects: [{ kind: 'User', name: 'admin' }],
      createdAt: '2024-01-01T00:00:00Z',
    };
    expect(binding.subjects).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// ArgoCD types
// ---------------------------------------------------------------------------

describe('ArgoCD types', () => {
  it('ArgoInstance can be created', () => {
    const instance: ArgoInstance = {
      id: 'argo-1',
      name: 'production-argo',
      url: 'https://argocd.example.com',
      clusterId: 'c-1',
      clusterName: 'prod',
      version: '2.10.0',
      applicationCount: 15,
      status: 'connected',
      createdAt: '2024-01-01T00:00:00Z',
    };
    expect(instance.status).toBe('connected');
  });

  it('ArgoApplication can be created', () => {
    const app: ArgoApplication = {
      name: 'my-app',
      namespace: 'argocd',
      project: 'default',
      clusterId: 'c-1',
      clusterName: 'prod',
      argoInstanceId: 'argo-1',
      syncStatus: 'Synced',
      healthStatus: 'Healthy',
      source: {
        repoURL: 'https://github.com/org/repo',
        path: 'charts/app',
        targetRevision: 'main',
      },
      destination: {
        server: 'https://kubernetes.default.svc',
        namespace: 'default',
      },
      createdAt: '2024-01-01T00:00:00Z',
    };
    expect(app.syncStatus).toBe('Synced');
  });
});

// ---------------------------------------------------------------------------
// Metrics types
// ---------------------------------------------------------------------------

describe('Metrics types', () => {
  it('TimeSeriesPoint can be created', () => {
    const point: TimeSeriesPoint = {
      timestamp: '2024-01-01T00:00:00Z',
      value: 42.5,
    };
    expect(point.value).toBe(42.5);
  });

  it('MetricsSummary can be created', () => {
    const summary: MetricsSummary = {
      cpuUsage: 8000,
      cpuCapacity: 20000,
      cpuPercentage: 40,
      memoryUsage: 32000000000,
      memoryCapacity: 64000000000,
      memoryPercentage: 50,
      podCount: 100,
      podCapacity: 550,
      nodeCount: 5,
      networkReceive: 1000000,
      networkTransmit: 500000,
      diskUsage: 100000000000,
      diskCapacity: 500000000000,
    };
    expect(summary.cpuPercentage).toBe(40);
  });
});

// ---------------------------------------------------------------------------
// Settings types
// ---------------------------------------------------------------------------

describe('Settings types', () => {
  it('SSOProvider can be created', () => {
    const provider: SSOProvider = {
      id: 'sso-1',
      provider: 'corporate-sso',
      type: 'oidc',
      name: 'Corporate SSO',
      enabled: true,
      config: { issuer: 'https://auth.example.com' },
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
    };
    expect(provider.type).toBe('oidc');
  });

  it('APIToken can be created', () => {
    const token: APIToken = {
      id: 'tok-1',
      name: 'CI Token',
      prefix: 'ast_',
      createdBy: 'admin',
      createdAt: '2024-01-01T00:00:00Z',
    };
    expect(token.prefix).toBe('ast_');
  });

  it('AuditLogEntry can be created', () => {
    const entry: AuditLogEntry = {
      id: 'log-1',
      action: 'create',
      resourceType: 'cluster',
      resourceName: 'prod-east',
      user: 'admin',
      sourceIP: '10.0.0.1',
      status: 'success',
      timestamp: '2024-01-01T00:00:00Z',
    };
    expect(entry.action).toBe('create');
  });

  it('ActivityEvent can be created', () => {
    const event: ActivityEvent = {
      id: 'evt-1',
      type: 'cluster',
      action: 'connected',
      message: 'Cluster prod-east connected',
      timestamp: '2024-01-01T00:00:00Z',
    };
    expect(event.type).toBe('cluster');
  });
});
