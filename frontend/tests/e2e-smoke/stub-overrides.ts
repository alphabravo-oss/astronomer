/**
 * P7.1 — Hand-written stub overrides for the route-smoke crawl.
 *
 * The generated OpenAPI stubs (openapi-stubs.generated.json) answer every
 * documented GET with type-zero skeleton data; the handful of endpoints here
 * need real-ish data for pages to render meaningfully (auth/session, feature
 * gates, the smoke cluster the $id fixtures point at). Bodies are snake_case:
 * this is wire format, the axios camelize interceptor converts on read.
 *
 * Keep this list SHORT (plan P7.1 step 4): a page that crashes on skeleton
 * data is a real error-handling bug — fix it in src, not here.
 */

export type StubOverride = {
  method: string;
  /** Request pathname with the trailing slash stripped, or a RegExp over it. */
  path: string | RegExp;
  body: unknown | ((url: URL) => unknown);
  status?: number;
};

export const SMOKE_CLUSTER_ID = 'c-smoke-1';

const now = '2026-01-01T00:00:00Z';

/** Wire-format (snake_case) admin identity for GET /auth/me/. */
const adminUserWire = {
  id: 'user-admin',
  username: 'admin',
  email: 'admin@example.com',
  display_name: 'Admin User',
  provider: 'local',
  global_roles: ['admin'],
  is_superuser: true,
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  must_change_password: false,
  last_login: now,
  created_at: now,
};

/**
 * Store-shape (camelCase) twin of the same identity, for the seedAuth
 * localStorage envelope — byte-compatible with the persisted auth store.
 */
export const adminStoreUser = {
  id: 'user-admin',
  username: 'admin',
  email: 'admin@example.com',
  displayName: 'Admin User',
  provider: 'local',
  globalRoles: ['admin'],
  isSuperuser: true,
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  mustChangePassword: false,
  lastLogin: now,
  createdAt: now,
};

/** The cluster every `$id: 'c-smoke-1'` fixture URL resolves to. */
const smokeCluster = {
  id: SMOKE_CLUSTER_ID,
  name: 'smoke-east',
  display_name: 'Smoke East',
  description: 'Route-smoke fixture cluster',
  status: 'active',
  health: { status: 'active', last_check: now, components: [] },
  provider: 'aws',
  environment: 'production',
  region: 'us-east-1',
  distribution: 'eks',
  kubernetes_version: '1.31',
  node_count: 3,
  pod_count: 42,
  namespace_count: 8,
  cpu_capacity: 24,
  cpu_usage: 6,
  cpu_percentage: 25,
  memory_capacity: 96,
  memory_usage: 32,
  memory_percentage: 33,
  labels: {},
  annotations: {},
  agent_version: 'route-smoke',
  agent_connected: true,
  last_heartbeat: now,
  created_at: now,
  updated_at: now,
  is_local: false,
};

export const overrides: StubOverride[] = [
  { method: 'GET', path: '/api/v1/auth/me', body: { data: adminUserWire } },
  { method: 'GET', path: '/api/v1/auth/sso/providers', body: { data: [] } },
  // Every gated section renders instead of the "feature disabled" panel.
  {
    method: 'GET',
    path: '/api/v1/settings/features',
    body: {
      data: {
        'feature.catalog': true,
        'feature.projects': true,
        'feature.monitoring': true,
        'feature.argocd': true,
        'feature.security': true,
        'feature.backups': true,
      },
    },
  },
  // SSE ticket mint (live events stream + pod/log watches). The stream
  // itself is answered by the Accept-header rule in stubs.ts.
  {
    method: 'POST',
    path: '/api/v1/streams/tickets',
    body: { data: { ticket: 'route-smoke-ticket', expires_at: '2999-01-01T00:00:00Z' } },
  },
  {
    method: 'GET',
    path: '/api/v1/clusters',
    body: { data: [smokeCluster], count: 1, next: null, previous: null },
  },
  { method: 'GET', path: `/api/v1/clusters/${SMOKE_CLUSTER_ID}`, body: { data: smokeCluster } },
  { method: 'GET', path: '/api/v1/extensions/mounts', body: { data: [] } },
  // The remaining entries paper over spec-vs-consumer SHAPE gaps, not missing
  // data: the generated zero bodies for these endpoints (weak `{}` schemas or
  // `{items: []}` pagination shells) do not match what the client actually
  // consumes (`res.data.data` arrays / fully-shaped detail objects).
  { method: 'GET', path: `/api/v1/clusters/${SMOKE_CLUSTER_ID}/registries`, body: { data: [] } },
  {
    method: 'GET',
    path: '/api/v1/cloud-credentials/providers',
    body: { data: [{ provider: 'aws', display_name: 'AWS', description: '', fields: [] }] },
  },
  {
    method: 'GET',
    path: `/api/v1/projects/${SMOKE_CLUSTER_ID}/cloud-credentials`,
    body: { data: [] },
  },
  {
    method: 'GET',
    path: `/api/v1/projects/${SMOKE_CLUSTER_ID}/cloud-credentials/cred-smoke-1`,
    body: {
      data: {
        id: 'cred-smoke-1',
        project_id: SMOKE_CLUSTER_ID,
        name: 'smoke-cred',
        provider: 'aws',
        description: '',
        config: {},
        target_refs: [],
        created_at: now,
        updated_at: now,
      },
    },
  },
  {
    method: 'GET',
    path: `/api/v1/clusters/${SMOKE_CLUSTER_ID}/service-mesh/mtls`,
    body: {
      data: { cluster_id: SMOKE_CLUSTER_ID, mesh: 'istio', mtls_coverage_pct: 0, total_count: 0, rows: [] },
    },
  },
  {
    method: 'GET',
    path: `/api/v1/cluster-templates/${SMOKE_CLUSTER_ID}`,
    body: {
      data: {
        id: SMOKE_CLUSTER_ID,
        name: 'smoke-template',
        description: 'Route-smoke fixture template',
        environment: 'development',
        spec: {
          environment: 'development',
          tools: [],
          labels: [],
          default_project: {},
          registration_policy: { require_approval: false, token_rotation_days: 90 },
        },
        created_at: now,
        updated_at: now,
      },
    },
  },
  {
    // Weak `{}` schema in the spec; the page maps operator/effective/desired
    // CIDR arrays directly.
    method: 'GET',
    path: `/api/v1/clusters/${SMOKE_CLUSTER_ID}/apiserver-allowlist`,
    body: {
      data: {
        mode: 'monitor',
        operator_cidrs: [],
        astronomer_egress: [],
        effective: [],
        desired: [],
        drift: false,
        sync_status: 'synced',
        detected_provider: 'aws',
        last_reconciled_at: now,
      },
    },
  },
  // Undocumented (not in docs/openapi.yaml) DETAIL endpoints: the generic
  // `{data: []}` fallback is truthy and defeats the pages' `if (!x)` guards,
  // so each needs a minimally real-shaped body.
  {
    method: 'GET',
    path: '/api/v1/backups/restores/restore-smoke-1',
    body: {
      data: {
        id: 'restore-smoke-1',
        backup_id: 'run-smoke-1',
        cluster_id: SMOKE_CLUSTER_ID,
        velero_restore_name: 'smoke-restore',
        phase: 'Completed',
        status: 'completed',
        namespace_mapping: {},
        included_namespaces: [],
        created_at: now,
        updated_at: now,
      },
    },
  },
  {
    // Spec documents a flat `event_filters` shape here, but the client
    // consumes `filters.events` — emit what the client (and Go handler) use.
    method: 'GET',
    path: `/api/v1/admin/webhooks/${SMOKE_CLUSTER_ID}`,
    body: {
      data: {
        id: SMOKE_CLUSTER_ID,
        name: 'smoke-webhook',
        url: 'https://example.test/hook',
        template: 'generic',
        secret: '',
        enabled: true,
        filters: { events: [] },
        created_at: now,
        updated_at: now,
      },
    },
  },
  {
    method: 'GET',
    path: '/api/v1/security/scans/scan-smoke-1',
    body: {
      data: {
        id: 'scan-smoke-1',
        cluster_id: SMOKE_CLUSTER_ID,
        cluster_scan_name: 'smoke-scan',
        status: 'completed',
        findings: [],
        created_at: now,
        updated_at: now,
        completed_at: now,
      },
    },
  },
  {
    method: 'GET',
    path: `/api/v1/fleet-operations/${SMOKE_CLUSTER_ID}`,
    body: {
      data: {
        id: SMOKE_CLUSTER_ID,
        name: 'smoke-fleet-op',
        description: '',
        operation_type: 'agent_upgrade',
        operation_spec: {},
        selector: null,
        strategy: 'rolling',
        max_concurrent: 1,
        on_error: 'pause',
        respect_maintenance_windows: false,
        status: 'succeeded',
        total_clusters: 1,
        completed_clusters: 1,
        failed_clusters: 0,
        skipped_clusters: 0,
        started_at: now,
        completed_at: now,
        created_at: now,
        updated_at: now,
      },
    },
  },
  {
    method: 'GET',
    path: `/api/v1/clusters/${SMOKE_CLUSTER_ID}/workloads/deployment/default/smoke-app`,
    body: {
      data: {
        name: 'smoke-app',
        namespace: 'default',
        kind: 'Deployment',
        cluster_id: SMOKE_CLUSTER_ID,
        cluster_name: 'smoke-east',
        status: 'healthy',
        ready: '3/3',
        up_to_date: 3,
        available: 3,
        replicas: 3,
        desired_replicas: 3,
        images: [],
        labels: {},
        annotations: {},
        created_at: now,
        age: '1d',
      },
    },
  },
];
