import type * as apiClient from './api';

// ============================================================
// Query Key Factories
// ============================================================
//
// Single source of truth for TanStack Query cache keys. Every `useQuery` and
// every `invalidateQueries`/`setQueryData` should reference an entry here so
// reads and invalidations can never drift apart. Do NOT inline `queryKey: [...]`
// arrays at call sites — add a factory entry instead (enforced by lint).

export const queryKeys = {
  featureFlags: ['settings', 'features'] as const,
  clusters: {
    all: ['clusters'] as const,
    list: (params?: Record<string, unknown>) => ['clusters', 'list', params] as const,
    detail: (id: string) => ['clusters', 'detail', id] as const,
    nodes: (id: string) => ['clusters', id, 'nodes'] as const,
    nodeDetail: (id: string, nodeName: string) => ['clusters', id, 'nodes', nodeName] as const,
    namespaces: (id: string) => ['clusters', id, 'namespaces'] as const,
    conditions: (id: string) => ['clusters', id, 'conditions'] as const,
    conditionRemediation: (id: string) => ['clusters', id, 'condition-remediation'] as const,
    // Params is part of the key because callers ask for different
    // limits (overview wants the latest 10; the events page + sidebar
    // both want 200). Without the params in the cache key, whichever
    // hook mounted last wins and the sidebar count flaps between 10
    // and 200 as the user navigates Overview ↔ Events.
    events: (id: string, params?: Record<string, unknown>) =>
      ['clusters', id, 'events', params] as const,
    // Params (namespace filter) is part of the key so different namespaces
    // don't collide on one cache entry. Use `podsAll` to invalidate every
    // variant at once (3-element prefix), since `pods(id)` now resolves to a
    // 4-element key that wouldn't match the param variants.
    pods: (id: string, params?: Record<string, unknown>) => ['clusters', id, 'pods', params] as const,
    podsAll: (id: string) => ['clusters', id, 'pods'] as const,
    metrics: (id: string, range?: string) => ['clusters', id, 'metrics', range] as const,
    metricsSummary: (id: string) => ['clusters', id, 'metrics', 'summary'] as const,
    // Two-element prefix that matches every `list(params)` variant at once —
    // used by live-events to patch/invalidate all cached list pages. Do NOT use
    // `list()` for invalidation: that produces ['clusters','list',undefined] and
    // would only match the param-less query, not the param variants.
    listAll: ['clusters', 'list'] as const,
  },
  clusterPages: {
    appsInstalled: (id: string) => ['clusters', id, 'apps', 'installed'] as const,
    appCatalogBrowse: (query: string) => ['catalog', 'browse', query] as const,
    appCatalogRecommended: ['catalog', 'recommended'] as const,
    imageVulnSummary: (id: string) => ['clusters', id, 'image-vulns', 'summary'] as const,
    imageVulnImages: (id: string, namespace: string) =>
      ['clusters', id, 'image-vulns', 'images', namespace] as const,
    imageVulnReport: (id: string, reportId: string, severity: string) =>
      ['clusters', id, 'image-vulns', 'report', reportId, severity] as const,
    imageVulnHistory: (id: string, hours: number) =>
      ['clusters', id, 'image-vulns', 'history', hours] as const,
    imageVulnReportHistory: (id: string, reportId: string) =>
      ['clusters', id, 'image-vulns', 'report-history', reportId] as const,
    imageVulnDiff: (id: string, hours: number) =>
      ['clusters', id, 'image-vulns', 'diff', hours] as const,
    imageVulnProgress: (id: string) => ['clusters', id, 'image-vulns', 'progress'] as const,
    apiserverAllowlist: (id: string) => ['clusters', id, 'apiserver-allowlist'] as const,
    apiserverAllowlistSnapshots: (id: string) =>
      ['clusters', id, 'apiserver-allowlist-snapshots'] as const,
    registries: (id: string) => ['clusters', id, 'registries'] as const,
    mirroredIngressClasses: (id: string) => ['clusters', id, 'mirrored', 'ingress-classes'] as const,
    mirroredGatewayClasses: (id: string) => ['clusters', id, 'mirrored', 'gateway-classes'] as const,
    mirroredNetworkPolicies: (id: string) => ['clusters', id, 'mirrored', 'network-policies'] as const,
    mirroredResourceQuotas: (id: string) => ['clusters', id, 'mirrored', 'resource-quotas'] as const,
    mirroredLimitRanges: (id: string) => ['clusters', id, 'mirrored', 'limit-ranges'] as const,
    serviceMeshDetection: (id: string) => ['clusters', id, 'service-mesh'] as const,
    serviceMeshInventory: (id: string) => ['clusters', id, 'service-mesh', 'inventory'] as const,
    serviceMeshMtls: (id: string) => ['clusters', id, 'service-mesh', 'mtls'] as const,
    veleroStatus: (id: string) => ['clusters', id, 'velero-status'] as const,
    snapshots: (id: string) => ['clusters', id, 'snapshots'] as const,
    snapshotSchedules: (id: string) => ['clusters', id, 'snapshot-schedules'] as const,
    templates: ['cluster-templates'] as const,
    templateBinding: (id: string) => ['clusters', id, 'template'] as const,
    workloadKind: (id: string, kind: string) => ['clusters', id, 'workloads', kind] as const,
    vulnerabilitySummary: (id: string) => ['clusters', id, 'vulnerabilities', 'summary'] as const,
    serviceMeshHeader: (id: string) => ['clusters', id, 'service-mesh', 'header'] as const,
    registrationStatus: (id: string) => ['cluster-registration-status', id] as const,
    registrationManifest: (id: string) => ['clusters', id, 'registration-manifest'] as const,
  },
  workloads: {
    all: ['workloads'] as const,
    list: (clusterId: string, params?: Record<string, unknown>) =>
      ['workloads', clusterId, 'list', params] as const,
    detail: (clusterId: string, kind: string, ns: string, name: string) =>
      ['workloads', clusterId, kind, ns, name] as const,
    pods: (clusterId: string, kind: string, ns: string, name: string) =>
      ['workloads', clusterId, kind, ns, name, 'pods'] as const,
    metrics: (clusterId: string, kind: string, ns: string, name: string, range?: string) =>
      ['workloads', clusterId, kind, ns, name, 'metrics', range] as const,
  },
  podLogs: (clusterId: string, ns: string, pod: string, container?: string) =>
    ['pod-logs', clusterId, ns, pod, container] as const,
  // Initial pod-logs fetch — extends the base podLogs key with the resolved
  // tail/since sentinels so flipping modes refetches instead of serving the
  // previous mode's cache. Kept byte-identical to the inline spread it replaces.
  podLogsFetch: (
    clusterId: string,
    ns: string,
    pod: string,
    container: string | undefined,
    tail: number | 'no-tail',
    since: number | 'no-since'
  ) => [...queryKeys.podLogs(clusterId, ns, pod, container), tail, since] as const,
  argocd: {
    all: ['argocd'] as const,
    instances: (clusterId?: string) => ['argocd', 'instances', clusterId] as const,
    instance: (instanceId: string) => ['argocd', 'instance', instanceId] as const,
    instanceHealth: (instanceId: string) => ['argocd', 'instance', instanceId, 'health'] as const,
    liveApps: (instanceId?: string) =>
      instanceId ? (['argocd', 'live-apps', instanceId] as const) : (['argocd', 'live-apps'] as const),
    cachedApplications: (params?: { instanceId?: string; limit?: number; offset?: number }) =>
      ['argocd', 'cached-applications', params] as const,
    dbApps: (instanceId: string) => ['argocd', 'db-apps', instanceId] as const,
    dbApp: (appId: string) => ['argocd', 'db-app', appId] as const,
    appManifests: (appId: string) => ['argocd', 'app-manifests', appId] as const,
    appHistory: (appId: string) => ['argocd', 'app-history', appId] as const,
    operations: ['argocd', 'operations'] as const,
    appOperations: (appId: string) => ['argocd', 'operations', 'for-app', appId] as const,
    projects: (instanceId: string) => ['argocd', 'projects', instanceId] as const,
    repos: (instanceId: string) => ['argocd', 'repos', instanceId] as const,
    managedClusters: (instanceId: string) => ['argocd', 'managed-clusters', instanceId] as const,
    orphanReport: (instanceId: string) => ['argocd', 'orphan-report', instanceId] as const,
    appsets: (instanceId: string) => ['argocd', 'appsets', instanceId] as const,
    clusterOwnership: (clusterId: string) => ['argocd', 'clusters', clusterId, 'ownership'] as const,
    applications: (params?: Record<string, unknown>) => ['argocd', 'applications', params] as const,
  },
  rbac: {
    all: ['rbac'] as const,
    globalRoles: ['rbac', 'global-roles'] as const,
    clusterRoles: (clusterId?: string) => ['rbac', 'cluster-roles', clusterId] as const,
    projectRoles: (projectId?: string) => ['rbac', 'project-roles', projectId] as const,
    bindings: (params?: Record<string, unknown>) => ['rbac', 'bindings', params] as const,
    myPermissions: (params?: apiClient.EffectivePermissionParams) => ['rbac', 'my-permissions', params] as const,
  },
  users: {
    all: ['users'] as const,
    current: ['users', 'current'] as const,
    list: (params?: Record<string, unknown>) => ['users', 'list', params] as const,
  },
  nativeRbac: {
    all: ['native-rbac'] as const,
    // userId is part of the key so the per-user filtered list and the unfiltered
    // list never collide on one cache entry.
    list: (userId?: string) => ['native-rbac', 'list', userId] as const,
  },
  settings: {
    general: ['settings', 'general'] as const,
    sso: ['settings', 'sso'] as const,
    tokens: ['settings', 'tokens'] as const,
    auditLogs: (params?: Record<string, unknown>) => ['settings', 'audit-logs', params] as const,
    webhookDeliveries: (webhookId: string) =>
      ['settings', 'webhooks', webhookId, 'deliveries'] as const,
    registrationTls: ['settings', 'registration-tls'] as const,
  },
  activity: (limit?: number) => ['activity', limit] as const,
  alerting: {
    all: ['alerting'] as const,
    rules: ['alerting', 'rules'] as const,
    events: (params?: Record<string, unknown>) => ['alerting', 'events', params] as const,
    channels: ['alerting', 'channels'] as const,
    silences: ['alerting', 'silences'] as const,
    // P-03 — Alertmanager-style inhibition rules.
    inhibitions: ['alerting', 'inhibitions'] as const,
    inhibition: (id: string) => ['alerting', 'inhibitions', id] as const,
  },
  // F-05 — external SIEM forwarders + per-forwarder status.
  siemForwarders: {
    all: ['siem-forwarders'] as const,
    list: ['siem-forwarders', 'list'] as const,
    detail: (id: string) => ['siem-forwarders', 'detail', id] as const,
    status: (id: string) => ['siem-forwarders', 'status', id] as const,
  },
  // F-05 — SCIM provisioning tokens.
  scimTokens: ['scim-tokens'] as const,
  // P-04 — Gatekeeper constraint authoring (per-cluster).
  gatekeeperConstraints: (clusterId: string) => ['gatekeeper-constraints', clusterId] as const,
  // Sprint 072 — read-only anomaly baselines for tuning.
  anomalyBaselines: {
    list: (params?: Record<string, unknown>) => ['anomaly-baselines', 'list', params] as const,
    detail: (id: string) => ['anomaly-baselines', 'detail', id] as const,
  },
  logging: {
    all: ['logging'] as const,
    outputs: ['logging', 'outputs'] as const,
    pipelines: ['logging', 'pipelines'] as const,
    operations: (params?: Record<string, unknown>) => ['logging', 'operations', params] as const,
    // Two-element prefix matching every `operations(params)` list variant and
    // the `operation(id)` detail rows at once — used by retry to invalidate all.
    operationsAll: ['logging', 'operations'] as const,
    operation: (id: string) => ['logging', 'operations', 'detail', id] as const,
  },
  clusterGroups: {
    all: ['cluster-groups'] as const,
  },
  vault: {
    connections: ['vault-connections'] as const,
  },
  agents: {
    fleet: ['agents', 'fleet'] as const,
    diagnostics: (clusterId: string | null) => ['agents', 'fleet', clusterId, 'diagnostics'] as const,
    operations: (clusterId: string | null) => ['agents', 'fleet', clusterId, 'operations'] as const,
  },
  extensions: {
    list: ['extensions'] as const,
    // §HostMounts — viewer-readable enabled-extension mounts. The host runtime
    // (ExtensionProvider) caches the /mounts/ projection under this key.
    mounts: ['extensions', 'mounts'] as const,
    // §DataProxy — per (extension, dataSource, context) Tier-1 data fetch.
    data: (name: string, dataSourceId: string, context?: Record<string, unknown>) =>
      ['ext', name, dataSourceId, context] as const,
  },
  adminOperations: {
    queues: ['admin', 'queues'] as const,
    dlq: (queue: string) => ['admin', 'queues', queue, 'dlq'] as const,
    outbox: (status: string) => ['admin', 'task-outbox', status] as const,
  },
  // F-05 — superuser security diagnostics.
  adminSecurity: {
    keyStatus: ['admin', 'key-status'] as const,
    shellSessions: ['admin', 'shell-sessions'] as const,
    shellSessionCommands: (sessionId: string) =>
      ['admin', 'shell-sessions', sessionId, 'commands'] as const,
  },
  storage: {
    all: ['storage'] as const,
    pvs: (clusterId: string) => ['storage', clusterId, 'pvs'] as const,
    pvcs: (clusterId: string) => ['storage', clusterId, 'pvcs'] as const,
    storageClasses: (clusterId: string) => ['storage', clusterId, 'storageclasses'] as const,
  },
  networking: {
    all: ['networking'] as const,
    services: (clusterId: string) => ['networking', clusterId, 'services'] as const,
    ingresses: (clusterId: string) => ['networking', clusterId, 'ingresses'] as const,
    networkPolicies: (clusterId: string) => ['networking', clusterId, 'networkpolicies'] as const,
    gateways: (clusterId: string) => ['networking', clusterId, 'gateways'] as const,
    httpRoutes: (clusterId: string) => ['networking', clusterId, 'httproutes'] as const,
    gatewayClasses: (clusterId: string) => ['networking', clusterId, 'gatewayclasses'] as const,
    grpcRoutes: (clusterId: string) => ['networking', clusterId, 'grpcroutes'] as const,
    tlsRoutes: (clusterId: string) => ['networking', clusterId, 'tlsroutes'] as const,
    tcpRoutes: (clusterId: string) => ['networking', clusterId, 'tcproutes'] as const,
    udpRoutes: (clusterId: string) => ['networking', clusterId, 'udproutes'] as const,
    referenceGrants: (clusterId: string) => ['networking', clusterId, 'referencegrants'] as const,
  },
  projects: {
    all: ['projects'] as const,
    list: (params?: Record<string, unknown>) => ['projects', 'list', params] as const,
    detail: (id: string) => ['projects', 'detail', id] as const,
  },
  catalog: {
    all: ['catalog'] as const,
    repositories: ['catalog', 'repositories'] as const,
    charts: (params?: Record<string, unknown>) => ['catalog', 'charts', params] as const,
    chartVersions: (chartId: string) => ['catalog', 'charts', chartId, 'versions'] as const,
    installed: (params?: Record<string, unknown>) => ['catalog', 'installed', params] as const,
    // App-install/upgrade modal — distinct endpoints from `chartVersions` above
    // (note the different array shapes), kept verbatim to preserve cache identity.
    installChartVersions: (chartId: string) => ['catalog', 'chart-versions', chartId] as const,
    installChartValues: (chartId: string, version?: string) =>
      ['catalog', 'chart-values', chartId, version] as const,
  },
  backups: {
    all: ['backups'] as const,
    list: ['backups', 'list'] as const,
    storage: ['backups', 'storage'] as const,
    schedules: ['backups', 'schedules'] as const,
  },
  security: {
    all: ['security'] as const,
    templates: ['security', 'templates'] as const,
    policies: ['security', 'policies'] as const,
    scans: (params?: Record<string, unknown>) => ['security', 'scans', params] as const,
  },
  tools: {
    all: ['tools'] as const,
    list: () => ['tools', 'list'] as const,
    detail: (slug: string) => ['tools', 'detail', slug] as const,
    clusterStatus: (clusterId: string) => ['tools', 'clusterStatus', clusterId] as const,
    preview: (toolSlug: string, clusterId: string, preset: string) =>
      ['tools', 'preview', toolSlug, clusterId, preset] as const,
    operation: (operationId: string) => ['tools', 'operation', operationId] as const,
  },
  generic: {
    all: ['generic'] as const,
    resources: (clusterId: string, resourceType: string) =>
      ['generic', clusterId, resourceType] as const,
  },
  k8s: {
    // Top-level prefix used to invalidate every k8s-proxy cache entry at once
    // (the per-resource yaml/resource keys live in `k8sQueryKeys` in hooks.ts).
    all: ['k8s'] as const,
  },
  cis: {
    scansAll: ['cis', 'scans'] as const,
  },
};
