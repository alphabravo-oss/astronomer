// ============================================================
// Astronomer Platform Types
// ============================================================

// --- API Response Types ---

export interface APIResponse<T> {
  data: T;
  message?: string;
  status: number;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  count?: number;
  next?: string | null;
  previous?: string | null;
  page: number;
  pageSize: number;
  totalPages: number;
}

export interface APIError {
  message: string;
  code: string;
  status: number;
  details?: Record<string, string>;
}

// --- Cluster Types ---

export type ClusterStatus = 'active' | 'connecting' | 'warning' | 'error' | 'disconnected' | 'provisioning';

export type ClusterProvider = 'aws' | 'gcp' | 'azure' | 'on-prem' | 'digitalocean' | 'other';

export type ClusterEnvironment = 'production' | 'staging' | 'development' | 'testing';

export interface ClusterHealth {
  status: ClusterStatus;
  message?: string;
  lastCheck: string;
  components: ClusterHealthComponent[];
}

export interface ClusterHealthComponent {
  name: string;
  status: 'healthy' | 'degraded' | 'unhealthy' | 'unknown';
  message?: string;
}

export type ClusterDistribution = 'k3s' | 'rke2' | 'eks' | 'aks' | 'gke' | 'openshift' | 'k8s' | '';

export interface Cluster {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  status: ClusterStatus;
  health: ClusterHealth;
  provider: ClusterProvider;
  environment: ClusterEnvironment;
  region: string;
  distribution: ClusterDistribution;
  kubernetesVersion: string;
  nodeCount: number;
  podCount: number;
  namespaceCount: number;
  cpuCapacity: number;
  cpuUsage: number;
  cpuPercentage: number;
  memoryCapacity: number;
  memoryUsage: number;
  memoryPercentage: number;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  agentVersion: string;
  lastHeartbeat: string;
  createdAt: string;
  updatedAt: string;
  directAccessEnabled?: boolean;
  // True for the management plane's own cluster (set by bootstrap
  // self-registration). Features requiring a remote tunnel — kubectl
  // shell, image-scan rescan, etc. — are hidden / disabled for is_local
  // clusters because the in-cluster local-agent path is best-effort.
  isLocal?: boolean;
  agentPrivilegeProfile?: 'viewer' | 'operator' | 'namespace-viewer' | 'namespace-operator' | 'custom' | 'admin' | string;
  argocd?: ClusterArgoCDSummary;
}

export type AgentFleetStatus = 'connected' | 'degraded' | 'disconnected';

export interface AgentOfflineBehavior {
  state: 'offline' | string;
  lastKnownAt?: string;
  stale: boolean;
  message: string;
  permittedQueuedOperations: string[];
  blockedOperations: string[];
}

export interface AgentFleetSummary {
  totalClusters: number;
  connected: number;
  degraded: number;
  disconnected: number;
  versions: Record<string, number>;
  profiles: Record<string, number>;
  statuses: Record<string, number>;
  compatibility: Record<string, number>;
  serverVersion: string;
  minimumSupportedAgentVersion: string;
  minimumCompatibleAgentVersion: string;
  generatedAt: string;
}

export interface AgentFleetItem {
  clusterId: string;
  clusterName: string;
  clusterDisplayName: string;
  clusterStatus: string;
  isLocal: boolean;
  agentStatus: AgentFleetStatus;
  agentId?: string;
  sessionId?: string;
  agentVersion?: string;
  kubernetesVersion?: string;
  distribution?: string;
  nodeCount: number;
  connectedAt?: string;
  lastPing?: string;
  lastHeartbeat?: string;
  disconnectedAt?: string;
  podName?: string;
  nodeName?: string;
  channelName?: string;
  privilegeProfile: 'viewer' | 'operator' | 'admin' | string;
  capabilities: Record<string, boolean>;
  compatibilityStatus: 'supported' | 'deprecated' | 'blocked' | 'unknown' | string;
  compatibilityMessage?: string;
  degradedReasons?: string[];
  recommendedAction?: string;
  offlineBehavior?: AgentOfflineBehavior;
}

export interface AgentFleetResponse {
  summary: AgentFleetSummary;
  items: AgentFleetItem[];
  limit: number;
  offset: number;
}

export interface AgentConnectionDiagnostic {
  id: string;
  agentId: string;
  sessionId: string;
  status: string;
  agentVersion?: string;
  connectedAt: string;
  lastPing?: string;
  disconnectedAt?: string;
  podName?: string;
  nodeName?: string;
  channelName?: string;
}

export interface AgentClusterConditionDiagnostic {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime: string;
  lastProbeTime?: string;
}

export interface AgentUpgradeRecommendation {
  currentVersion?: string;
  status: string;
  message: string;
}

export interface AgentArgoCDDiagnostic {
  registered: boolean;
  instanceCount: number;
  clusterSecretNames?: string[];
  serverUrls?: string[];
  lastUpdatedAt?: string;
}

export interface AgentLiveDiagnosticCheck {
  name: string;
  status: 'passed' | 'warning' | 'failed' | string;
  message: string;
}

export interface AgentLivePodDiagnostic {
  name: string;
  namespace: string;
  phase: string;
  nodeName?: string;
  ready: boolean;
  restartCount: number;
  containerImages?: string[];
}

export interface AgentLiveDiagnostics {
  collectedAt: string;
  deployment?: Record<string, unknown>;
  pods?: AgentLivePodDiagnostic[];
  events?: Array<Record<string, unknown>>;
  logs?: Array<Record<string, unknown>>;
  discovery?: Record<string, unknown>;
  checks?: AgentLiveDiagnosticCheck[];
  errors?: string[];
}

export interface AgentDiagnosticsResponse {
  generatedAt: string;
  agent: AgentFleetItem;
  recentConnections: AgentConnectionDiagnostic[];
  conditions: AgentClusterConditionDiagnostic[];
  argocd: AgentArgoCDDiagnostic;
  live?: AgentLiveDiagnostics;
  recommendations: string[];
  redactions: string[];
  upgradeRecommendation: AgentUpgradeRecommendation;
}

export type AgentSelfTestStatus = 'passed' | 'warning' | 'failed' | string;

export interface AgentSelfTestCheck {
  name: string;
  status: AgentSelfTestStatus;
  message: string;
}

export interface AgentSelfTestResponse {
  generatedAt: string;
  clusterId: string;
  clusterName: string;
  status: AgentSelfTestStatus;
  checks: AgentSelfTestCheck[];
  recommendations?: string[];
}

export interface AgentUpgradePlanRequest {
  targetVersion?: string;
  targetImage?: string;
  strategy?: string;
  canaryClusterIds?: string[];
  batchSize?: number;
  maxUnavailable?: number;
  rollbackImage?: string;
}

export interface AgentUpgradePlanResponse {
  clusterId: string;
  clusterName: string;
  currentVersion?: string;
  targetVersion: string;
  currentImage?: string;
  targetImage: string;
  rollbackImage?: string;
  privilegeProfile: string;
  strategy: string;
  canaryClusterIds?: string[];
  batchSize: number;
  maxUnavailable: number;
  ready: boolean;
  blockers?: string[];
  preflightChecks: string[];
  steps: string[];
  postUpgradeHealthChecks: string[];
  validation: string[];
  rollback: string[];
}

export interface AgentLifecycleOperation {
  id: string;
  clusterId: string;
  operationType: 'agent_upgrade' | string;
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled' | string;
  targetVersion: string;
  targetImage: string;
  currentVersion?: string;
  strategy: string;
  operationSpec?: Record<string, unknown>;
  requestedBy?: string;
  startedAt?: string;
  completedAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface AgentLifecycleOperationsResponse {
  items: AgentLifecycleOperation[];
  limit: number;
  offset: number;
}

export interface AgentUpgradeOperationResponse {
  operation: AgentLifecycleOperation;
  plan: AgentUpgradePlanResponse;
}

export interface ClusterArgoCDSummary {
  registered: boolean;
  instanceCount: number;
  clusterSecretNames: string[];
  baselineManagedBy: 'argocd' | 'argocd_pending' | 'helm' | 'local' | 'unknown' | string;
  baselineComponents?: ClusterBaselineComponentOwner[];
  drift?: ClusterArgoCDDriftSummary;
}

export interface ClusterArgoCDDriftSummary {
  appCount: number;
  syncedCount: number;
  outOfSyncCount: number;
  unknownSyncCount: number;
  healthyCount: number;
  progressingCount: number;
  degradedCount: number;
  unknownHealthCount: number;
  resourceCreatedCount: number;
  resourceChangedCount: number;
  resourcePrunedCount: number;
  lastSynced?: string;
  lastError?: string;
}

export interface ClusterBaselineComponentOwner {
  slug: string;
  name: string;
  namespace: string;
  applicationSetName: string;
  managedBy: 'argocd' | 'argocd_pending' | 'helm' | 'local' | 'unknown' | string;
}

// metav1.Condition-shaped row written by the health-check worker
// (internal/worker/tasks/health_check.go). Types currently emitted:
//   Connected           — agent heartbeat freshness
//   AgentReachable      — GET /version round-trip through the tunnel
//   GatewayAPISupported — gateway.networking.k8s.io/v1 discovery probe
export interface ClusterCondition {
  type: string;
  status: 'True' | 'False' | 'Unknown';
  reason: string;
  message: string;
  last_transition_time: string;
  last_probe_time: string;
}

// Sprint 086 — entries the cluster-condition remediation reconciler
// writes when it attempts to drive a False condition back to True.
// Shown in the cluster header as "Last action: $action — $outcome".
export interface ClusterConditionRemediationAttempt {
  id: string;
  cluster_id: string;
  condition_type: string;
  action: string;
  outcome: 'success' | 'failed' | 'skipped';
  error: string | null;
  detail: Record<string, unknown> | null;
  attempted_at: string;
}

export interface ClusterNode {
  name: string;
  status: 'Ready' | 'NotReady' | 'SchedulingDisabled';
  roles: string[];
  kubernetesVersion: string;
  os: string;
  architecture: string;
  containerRuntime: string;
  cpuCapacity: number;
  cpuUsage: number;
  memoryCapacity: number;
  memoryUsage: number;
  podCapacity: number;
  podCount: number;
  conditions: NodeCondition[];
  createdAt: string;
}

export interface NodeCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransition: string;
}

export interface NodeDetailCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastHeartbeat: string;
  lastTransition: string;
}

export interface NodeAddress {
  type: string;
  address: string;
}

export interface NodeTaint {
  key: string;
  value: string;
  effect: string;
}

export interface NodeImage {
  name: string;
  sizeBytes: number;
}

export interface NodePod {
  name: string;
  namespace: string;
  status: string;
  ready: string;
  restarts: number;
  createdAt: string;
  images: string[];
}

export interface NodeEvent {
  type: string;
  reason: string;
  message: string;
  count: number;
  firstTimestamp: string;
  lastTimestamp: string;
}

export interface NodeInfo {
  machineId: string;
  systemUuid: string;
  bootId: string;
  kernelVersion: string;
  osImage: string;
  containerRuntimeVersion: string;
  kubeletVersion: string;
  kubeProxyVersion: string;
  operatingSystem: string;
  architecture: string;
}

export interface NodeDetail {
  name: string;
  status: 'Ready' | 'NotReady' | 'SchedulingDisabled';
  roles: string[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  createdAt: string;
  nodeInfo: NodeInfo;
  cpuCapacity: number;
  cpuUsage: number;
  memoryCapacity: number;
  memoryUsage: number;
  podCapacity: number;
  podCount: number;
  addresses: NodeAddress[];
  conditions: NodeDetailCondition[];
  taints: NodeTaint[];
  images: NodeImage[];
  pods: NodePod[];
  events: NodeEvent[];
  unschedulable: boolean;
}

export interface ClusterEvent {
  id: string;
  type: 'Normal' | 'Warning';
  reason: string;
  message: string;
  involvedObject: {
    kind: string;
    name: string;
    namespace?: string;
  };
  count: number;
  firstTimestamp: string;
  lastTimestamp: string;
}

export interface ClusterRegistration {
  name: string;
  displayName: string;
  environment: ClusterEnvironment;
  description?: string;
  labels?: Record<string, string>;
  directAccessEnabled?: boolean;
  // Sprint 078 — registration wizard fields. distribution is a free-form
  // dropdown on the wizard; the backend accepts it but doesn't otherwise
  // gate on it. install_baseline is the Quick Start opt-in.
  distribution?: ClusterDistribution;
  region?: string;
  provider?: ClusterProvider;
  install_baseline?: boolean;
}

export interface ClusterRegistrationResponse {
  clusterId: string;
  token: string;
  installCommand: string;
  manifestUrl: string;
}

// --- Workload Types ---

export type WorkloadKind = 'Deployment' | 'StatefulSet' | 'DaemonSet' | 'Job' | 'CronJob' | 'ReplicaSet';

export type WorkloadStatus = 'Running' | 'Pending' | 'Failed' | 'Succeeded' | 'Unknown';

export interface Workload {
  name: string;
  namespace: string;
  kind: WorkloadKind;
  clusterId: string;
  clusterName: string;
  status: WorkloadStatus;
  ready: string; // e.g., "3/3"
  upToDate: number;
  available: number;
  replicas: number;
  desiredReplicas: number;
  images: string[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  createdAt: string;
  age: string;
}

export type PodPhase = 'Running' | 'Pending' | 'Succeeded' | 'Failed' | 'Unknown';

export interface Pod {
  name: string;
  namespace: string;
  clusterId: string;
  phase: PodPhase;
  status: string;
  ready: string;
  restarts: number;
  node: string;
  ip: string;
  containers: Container[];
  conditions: PodCondition[];
  createdAt: string;
  age: string;
}

export interface Container {
  name: string;
  image: string;
  status: 'running' | 'waiting' | 'terminated';
  ready: boolean;
  restartCount: number;
  cpuRequest?: string;
  cpuLimit?: string;
  memoryRequest?: string;
  memoryLimit?: string;
  ports?: ContainerPort[];
}

export interface ContainerPort {
  name?: string;
  containerPort: number;
  protocol: string;
}

export interface PodCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransition: string;
}

export interface PodLog {
  timestamp: string;
  message: string;
  container: string;
  level?: 'info' | 'warn' | 'error' | 'debug';
}

// --- Namespace Types ---

export interface Namespace {
  name: string;
  clusterId: string;
  status: 'Active' | 'Terminating';
  labels: Record<string, string>;
  annotations: Record<string, string>;
  podCount: number;
  cpuUsage: number;
  cpuLimit: number;
  memoryUsage: number;
  memoryLimit: number;
  createdAt: string;
}

// --- Project Types ---

export interface Project {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  // The Go backend models projects as cluster-scoped — one cluster per
  // project — so the canonical key is `cluster_id` (singular). The
  // old `clusterIds` array was a vestige from an earlier multi-cluster
  // design; kept here as optional for backward compatibility with any
  // legacy components that haven't been migrated yet.
  clusterId?: string;
  clusterIds?: string[];
  namespaces: string[];
  members: ProjectMember[];
  resourceQuota?: ResourceQuota;
  createdAt: string;
  updatedAt: string;
}

export interface ProjectMember {
  userId: string;
  username: string;
  role: string;
}

export interface ResourceQuota {
  cpuLimit: string;
  memoryLimit: string;
  podLimit: number;
  storageLimit: string;
}

// --- User & Auth Types ---

export interface User {
  id: string;
  username: string;
  email: string;
  displayName: string;
  avatarUrl?: string;
  provider: 'local' | 'github' | 'google' | 'oidc' | 'saml';
  globalRoles: string[];
  isSuperuser?: boolean;
  is_superuser?: boolean;
  roles?: {
    global: UserRoleBinding[];
    cluster: UserRoleBinding[];
    project: UserRoleBinding[];
  };
  enabled: boolean;
  lastLogin: string;
  createdAt: string;
  // True when an admin has forced a password rotation. The dashboard
  // middleware redirects any other route to /auth/change-password while this
  // is set. Backend emits the field as `must_change_password` (snake_case);
  // the user object is stored as-received, so callers read the snake_case key.
  must_change_password?: boolean;
  mustChangePassword?: boolean;
}

export interface UserRoleBinding {
  id: string;
  roleId?: string;
  role_id?: string;
  roleName?: string;
  role_name?: string;
  roleRules?: Array<{ resource: string; verbs: string[] }>;
  role_rules?: Array<{ resource: string; verbs: string[] }>;
  group?: string;
  clusterId?: string;
  cluster_id?: string;
  projectId?: string;
  project_id?: string;
}

export interface GlobalRole {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  builtin: boolean;
  rules: PolicyRule[];
  createdAt: string;
}

export interface ClusterRole {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  clusterId: string;
  clusterName: string;
  builtin: boolean;
  rules: PolicyRule[];
  createdAt: string;
}

export interface ProjectRole {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  projectId: string;
  projectName: string;
  builtin: boolean;
  rules: PolicyRule[];
  createdAt: string;
}

export interface PolicyRule {
  apiGroups: string[];
  resources: string[];
  verbs: string[];
  resourceNames?: string[];
}

export interface RoleBinding {
  id: string;
  name: string;
  roleType: 'global' | 'cluster' | 'project';
  roleName: string;
  subjects: RoleBindingSubject[];
  scope?: {
    clusterId?: string;
    clusterName?: string;
    projectId?: string;
    projectName?: string;
  };
  createdAt: string;
}

export interface RoleBindingSubject {
  kind: 'User' | 'Group' | 'ServiceAccount';
  name: string;
  namespace?: string;
}

export interface RBACEngineRule {
  resource: string;
  verbs: string[];
}

export interface EffectivePermissionSource {
  scope: string;
  bindingId?: string;
  roleId?: string;
  roleName?: string;
  clusterId?: string;
  projectId?: string;
}

export interface EffectivePermissionGrant {
  resource: string;
  verb: string;
  appliesToContext?: boolean;
  sources: EffectivePermissionSource[];
}

export interface EffectivePermissionBinding {
  scope: string;
  bindingId?: string;
  roleId?: string;
  roleName?: string;
  group?: string;
  clusterId?: string;
  projectId?: string;
  rules: RBACEngineRule[];
}

export interface EffectivePermissionContext {
  clusterId?: string;
  projectId?: string;
  namespace?: string;
  namespaceScopedBindingsSupported: boolean;
  warnings?: string[];
}

export interface EffectivePermissionResponse {
  subject: {
    userId: string;
    self: boolean;
  };
  context: EffectivePermissionContext;
  bindings: EffectivePermissionBinding[];
  permissions: EffectivePermissionGrant[];
}

export interface PermissionPreviewRequest {
  scope: 'global' | 'cluster' | 'project';
  roleId?: string;
  templateName?: string;
  rules?: RBACEngineRule[];
  clusterId?: string;
  projectId?: string;
}

export interface PermissionPreviewResponse {
  scope: string;
  roleId?: string;
  roleName?: string;
  templateName?: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical' | string;
  warnings?: string[];
  permissions: EffectivePermissionGrant[];
  sensitiveFlags: {
    wildcard: boolean;
    canMutate: boolean;
    canDelete: boolean;
    canExec: boolean;
    canProxy: boolean;
    canReadSecrets: boolean;
    canManageRbac: boolean;
    canRestore: boolean;
  };
}

// --- ArgoCD Types ---

export type ArgoSyncStatus = 'Synced' | 'OutOfSync' | 'Unknown';

export type ArgoHealthStatus = 'Healthy' | 'Degraded' | 'Progressing' | 'Suspended' | 'Missing' | 'Unknown';

export interface ArgoInstance {
  id: string;
  name: string;
  url: string;
  clusterId: string;
  clusterName: string;
  version: string;
  applicationCount: number;
  status: 'connected' | 'disconnected';
  createdAt: string;
}

export interface ArgoApplication {
  name: string;
  namespace: string;
  project: string;
  clusterId: string;
  clusterName: string;
  argoInstanceId: string;
  syncStatus: ArgoSyncStatus;
  healthStatus: ArgoHealthStatus;
  source: ArgoApplicationSource;
  destination: ArgoApplicationDestination;
  syncPolicy?: ArgoSyncPolicy;
  lastSyncedAt?: string;
  createdAt: string;
}

export interface ArgoApplicationSource {
  repoURL: string;
  path: string;
  targetRevision: string;
  chart?: string;
  helm?: {
    valueFiles?: string[];
    parameters?: { name: string; value: string }[];
  };
}

export interface ArgoApplicationDestination {
  server: string;
  namespace: string;
  name?: string;
}

export interface ArgoSyncPolicy {
  automated?: {
    prune: boolean;
    selfHeal: boolean;
  };
  syncOptions?: string[];
}

export interface ArgoManagedClusterOwnershipSummary {
  argocdInstanceId: string;
  clusterSecretName: string;
  serverUrl: string;
  labels: Record<string, string>;
  updatedAt: string;
}

export interface ArgoBaselineOwnershipDecision {
  id: string;
  decision: 'adopt' | 'leave_local' | 'replace' | string;
  reason: string;
  expiresAt?: string;
  decidedById?: string;
  updatedAt: string;
}

export interface ArgoBaselineComponentOwnership {
  slug: string;
  name: string;
  namespace: string;
  applicationSetName: string;
  desiredOwner: string;
  observedOwner: string;
  state: 'argocd_owned' | 'legacy_helm' | 'local_manual' | 'external_argocd' | 'unmanaged' | 'migration_required' | string;
  options: Array<'adopt' | 'leave_local' | 'replace' | string>;
  decision?: ArgoBaselineOwnershipDecision;
}

export interface ArgoClusterOwnershipResponse {
  clusterId: string;
  clusterName: string;
  registered: boolean;
  managedClusters: ArgoManagedClusterOwnershipSummary[];
  components: ArgoBaselineComponentOwnership[];
  generatedAt: string;
}

export interface ArgoBaselineOwnershipDecisionRequest {
  decision: 'adopt' | 'leave_local' | 'replace';
  reason?: string;
  expiresAt?: string;
}

// --- Metrics Types ---

export interface TimeSeriesPoint {
  timestamp: string;
  value: number;
}

export interface MetricsSeries {
  name: string;
  label?: string;
  unit: string;
  data: TimeSeriesPoint[];
}

export interface MetricsData {
  cpuUsage: MetricsSeries;
  cpuCapacity: MetricsSeries;
  memoryUsage: MetricsSeries;
  memoryCapacity: MetricsSeries;
  networkReceive: MetricsSeries;
  networkTransmit: MetricsSeries;
  diskUsage: MetricsSeries;
  podCount: MetricsSeries;
}

export interface MetricsSummary {
  cpuUsage: number;
  cpuCapacity: number;
  cpuPercentage: number;
  memoryUsage: number;
  memoryCapacity: number;
  memoryPercentage: number;
  podCount: number;
  podCapacity: number;
  nodeCount: number;
  networkReceive: number;
  networkTransmit: number;
  diskUsage: number;
  diskCapacity: number;
}

// --- Settings Types ---

export interface SSOProvider {
  id: string;
  provider: string;
  type: 'github' | 'google' | 'oidc';
  name: string;
  enabled: boolean;
  config: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

export interface APIToken {
  id: string;
  name: string;
  description?: string;
  prefix: string;
  expiresAt?: string;
  lastUsedAt?: string;
  createdBy: string;
  createdAt: string;
}

export interface AuditLogEntry {
  id: string;
  userId?: string | null;
  action: string;
  // Migration 063 — action_class distinguishes read-side from mutation rows.
  actionClass?: 'mutation' | 'read' | 'auth' | 'system';
  resourceType: string;
  resourceId?: string;
  resourceName: string;
  source?: string;
  correlationId?: string;
  actorAuthMethod?: string;
  httpMethod?: string;
  path?: string;
  statusCode?: number;
  durationMs?: number;
  requestId?: string;
  ipAddress?: string | null;
  user: string;
  userAgent?: string;
  sourceIP: string;
  status: 'success' | 'failure' | 'error';
  detail?: Record<string, unknown>;
  details?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
  timestamp: string;
}

// --- Alerting Types ---

export type AlertSeverity = 'critical' | 'warning' | 'info';

export type AlertRuleType = 'threshold' | 'anomaly' | 'absence' | 'change';

// Sprint 072 — rule_kind switches the evaluator path.
// "threshold" is the existing static-threshold logic; "anomaly"
// uses the rolling-baseline + stddev check from anomaly_baselines.
export type AlertRuleKind = 'threshold' | 'anomaly';

// Direction the anomaly rule fires on:
//  - above:  current > mean + N*stddev
//  - below:  current < mean - N*stddev
//  - either: |current - mean| > N*stddev
export type AnomalyDirection = 'above' | 'below' | 'either';

export interface AlertRule {
  id: string;
  name: string;
  description?: string;
  type: AlertRuleType;
  severity: AlertSeverity;
  clusterId?: string;
  clusterName?: string;
  namespace?: string;
  enabled: boolean;
  query: string;
  threshold?: number;
  duration: string;
  activeAlerts: number;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  notificationChannelIds: string[];
  // Sprint 072 anomaly fields. ruleKind defaults to 'threshold'
  // server-side so reading an old rule never returns undefined.
  ruleKind?: AlertRuleKind;
  metric?: string;
  anomalyStddev?: number;
  anomalyWindowSeconds?: number;
  anomalyMinSamples?: number;
  anomalyDirection?: AnomalyDirection;
  createdAt: string;
  updatedAt: string;
}

// AnomalyBaseline mirrors the read-only /api/v1/anomaly-baselines/
// surface. The recompute worker is the sole writer; the UI just
// renders these for tuning purposes.
export interface AnomalyBaseline {
  id: string;
  clusterId: string;
  metric: string;
  windowSeconds: number;
  sampleCount: number;
  mean: number;
  stddev: number;
  min: number;
  max: number;
  p50: number;
  p95: number;
  p99: number;
  lastValue: number;
  lastValueAt: string | null;
  updatedAt: string;
}

export type AlertEventStatus = 'firing' | 'acknowledged' | 'resolved' | 'silenced';

export interface AlertEvent {
  id: string;
  ruleId: string;
  ruleName: string;
  severity: AlertSeverity;
  status: AlertEventStatus;
  message: string;
  clusterId?: string;
  clusterName?: string;
  namespace?: string;
  resource?: string;
  labels: Record<string, string>;
  firedAt: string;
  acknowledgedAt?: string;
  acknowledgedBy?: string;
  resolvedAt?: string;
  resolvedBy?: string;
}

export type NotificationChannelType = 'slack' | 'email' | 'pagerduty' | 'webhook' | 'msteams';

export interface NotificationChannel {
  id: string;
  name: string;
  type: NotificationChannelType;
  enabled: boolean;
  config: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

export interface AlertSilence {
  id: string;
  reason: string;
  matchers: Record<string, string>;
  startsAt: string;
  endsAt: string;
  duration: string;
  createdBy: string;
  createdAt: string;
}

// --- Logging Types ---

export type LoggingOutputType = 'elasticsearch' | 'loki' | 'splunk' | 'cloudwatch' | 'datadog' | 's3' | 'syslog';

export interface LoggingOutput {
  id: string;
  name: string;
  type: LoggingOutputType;
  clusterId?: string;
  clusterName?: string;
  enabled: boolean;
  config: Record<string, string>;
  status: 'connected' | 'disconnected' | 'error';
  createdAt: string;
  updatedAt: string;
}

export interface LoggingPipeline {
  id: string;
  name: string;
  description?: string;
  clusterId?: string;
  clusterName?: string;
  namespaces: string[];
  outputIds: string[];
  outputNames: string[];
  filters: LoggingFilter[];
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface LoggingFilter {
  type: 'include' | 'exclude';
  field: string;
  pattern: string;
}

/**
 * A row from `GET /api/v1/logging/operations/`. The Go reconciler emits
 * snake_case keys; the axios response interceptor camelizes them before they
 * reach the type system, so this interface mirrors the post-camelize shape
 * (same pattern as `ArgoOperation`).
 */
export interface LoggingOperation {
  id: string;
  targetType: 'output' | 'pipeline' | string;
  targetKey: string;
  operation: 'apply' | 'delete' | string;
  status: 'pending' | 'running' | 'completed' | 'failed' | 'superseded' | string;
  payload?: Record<string, unknown>;
  errorMessage?: string;
  startedAt?: string | null;
  completedAt?: string | null;
  createdAt: string;
  updatedAt: string;
  // Returned only on the detail endpoint.
  events?: LoggingOperationEvent[];
}

export interface LoggingOperationEvent {
  id: string;
  level: string;
  stage: string;
  message: string;
  detail?: Record<string, unknown>;
  createdAt: string;
}

// --- Storage Types ---

export interface PersistentVolume {
  name: string;
  clusterId: string;
  clusterName: string;
  status: 'Available' | 'Bound' | 'Released' | 'Failed';
  capacity: string;
  accessModes: string[];
  reclaimPolicy: string;
  storageClass: string;
  volumeMode: string;
  claimRef?: string;
  createdAt: string;
}

export interface PersistentVolumeClaim {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  status: 'Pending' | 'Bound' | 'Lost';
  capacity: string;
  accessModes: string[];
  storageClass: string;
  volumeName?: string;
  createdAt: string;
}

export interface StorageClass {
  name: string;
  clusterId: string;
  clusterName: string;
  provisioner: string;
  reclaimPolicy: string;
  volumeBindingMode: string;
  allowVolumeExpansion: boolean;
  isDefault: boolean;
  parameters: Record<string, string>;
  createdAt: string;
}

// --- Networking Types ---

export interface K8sService {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  type: 'ClusterIP' | 'NodePort' | 'LoadBalancer' | 'ExternalName';
  clusterIP: string;
  externalIP?: string;
  ports: ServicePort[];
  selector: Record<string, string>;
  createdAt: string;
}

export interface ServicePort {
  name?: string;
  port: number;
  targetPort: number | string;
  protocol: string;
  nodePort?: number;
}

export interface Ingress {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  ingressClass?: string;
  hosts: string[];
  paths: IngressPath[];
  tls: boolean;
  createdAt: string;
}

export interface IngressPath {
  host: string;
  path: string;
  pathType: string;
  serviceName: string;
  servicePort: number | string;
}

export interface NetworkPolicy {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  podSelector: Record<string, string>;
  policyTypes: string[];
  ingressRules: number;
  egressRules: number;
  createdAt: string;
}

// --- Gateway API Types ---
//
// Backend flatten functions live in internal/handler/resources.go
// (flattenGateway / flattenRouteResource / flattenGatewayClass /
// flattenReferenceGrant). The fields below mirror the JSON they emit.

export interface GatewayListener {
  name: string;
  protocol: string;
  port: number;
  hostname?: string;
}

export interface Gateway {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  gatewayClassName: string;
  listeners: GatewayListener[];
  listenerSummary: string[];
  listenerCount: number;
  addresses: string[];
  // Status of the Programmed/Accepted conditions, as raw "True"/"False"/"Unknown"
  // strings (empty when the status hasn't been published yet).
  programmed: string;
  accepted: string;
  createdAt: string;
}

export interface RouteParentRef {
  name: string;
  namespace?: string;
  sectionName?: string;
  kind?: string;
}

// Shared shape for HTTPRoute, GRPCRoute, TLSRoute, TCPRoute, UDPRoute. They
// differ in spec (HTTP rules vs raw L4) but agree on the metadata the UI
// needs: hostnames (when applicable), parent Gateways, and a rule count.
export interface GatewayRoute {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  hostnames: string[];
  parentRefs: RouteParentRef[];
  parentSummary: string[];
  ruleCount: number;
  createdAt: string;
}

export type HTTPRoute = GatewayRoute;
export type GRPCRoute = GatewayRoute;
export type TLSRoute = GatewayRoute;
export type TCPRoute = GatewayRoute;
export type UDPRoute = GatewayRoute;

export interface GatewayClass {
  name: string;
  clusterId: string;
  clusterName: string;
  controllerName: string;
  description: string;
  accepted: string;
  createdAt: string;
}

export interface ReferenceGrantFrom {
  group: string;
  kind: string;
  namespace: string;
}

export interface ReferenceGrantTo {
  group: string;
  kind: string;
  name: string;
}

export interface ReferenceGrant {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  from: ReferenceGrantFrom[];
  to: ReferenceGrantTo[];
  createdAt: string;
}

// --- Catalog / Helm Types ---

export type HelmRepoType = 'helm' | 'oci';

export interface HelmRepository {
  id: string;
  name: string;
  url: string;
  repoType: HelmRepoType;
  description?: string;
  isDefault: boolean;
  enabled: boolean;
  chartCount: number;
  lastSyncedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export type HelmChartCategory =
  | 'monitoring'
  | 'logging'
  | 'security'
  | 'database'
  | 'networking'
  | 'storage'
  | 'messaging'
  | 'ci-cd'
  | 'other';

export interface HelmChart {
  id: string;
  repository: string;
  repositoryName: string;
  name: string;
  displayName: string;
  description?: string;
  iconUrl?: string;
  category: HelmChartCategory;
  keywords: string[];
  latestVersion: string;
  createdAt: string;
  updatedAt: string;
}

export interface HelmChartVersion {
  id: string;
  chart: string;
  chartName: string;
  version: string;
  appVersion: string;
  readme?: string;
  defaultValues?: string;
  valuesSchema?: Record<string, unknown>;
  createdAt: string;
}

export type InstalledChartStatus = 'deployed' | 'failed' | 'pending-install' | 'pending-upgrade' | 'uninstalling';

export interface InstalledChart {
  id: string;
  cluster: string;
  clusterName: string;
  chartVersion: string;
  chartName: string;
  chartVersionLabel: string;
  releaseName: string;
  namespace: string;
  status: InstalledChartStatus;
  revision: number;
  installedBy: string;
  valuesOverride?: string;
  createdAt: string;
  updatedAt: string;
}

// --- Backup Types ---

export type BackupStorageType = 's3' | 'gcs' | 'azure' | 'minio';

export interface BackupStorageConfig {
  id: string;
  name: string;
  storageType: BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpointUrl?: string;
  isDefault: boolean;
  createdAt: string;
  updatedAt: string;
}

export type BackupType = 'full' | 'database' | 'config';

export type BackupStatus = 'pending' | 'in_progress' | 'completed' | 'failed';

export interface Backup {
  id: string;
  name: string;
  storage: string;
  storageName: string;
  backupType: BackupType;
  status: BackupStatus;
  filePath?: string;
  fileSizeBytes?: number;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
  createdBy: string;
  createdAt: string;
}

export interface BackupSchedule {
  id: string;
  name: string;
  storage: string;
  storageName: string;
  backupType: BackupType;
  cronExpression: string;
  retentionCount: number;
  enabled: boolean;
  lastBackup?: string;
  lastBackupStatus?: BackupStatus;
  createdAt: string;
  updatedAt: string;
}

// --- Security Types ---

export type PodSecurityLevel = 'privileged' | 'baseline' | 'restricted';

export interface PodSecurityTemplate {
  id: string;
  name: string;
  description?: string;
  isDefault: boolean;
  enforceLevel: PodSecurityLevel;
  enforceVersion?: string;
  auditLevel: PodSecurityLevel;
  auditVersion?: string;
  warnLevel: PodSecurityLevel;
  warnVersion?: string;
  exemptNamespaces: string[];
  exemptRuntimeClasses: string[];
  exemptUsernames: string[];
  createdAt: string;
  updatedAt: string;
}

export type SecurityPolicySyncStatus = 'synced' | 'pending' | 'failed' | 'unknown';

export interface ClusterSecurityPolicy {
  id: string;
  cluster: string;
  clusterName: string;
  template: string;
  templateName: string;
  enforceLevel: PodSecurityLevel;
  auditLevel: PodSecurityLevel;
  warnLevel: PodSecurityLevel;
  appliedAt?: string;
  syncStatus: SecurityPolicySyncStatus;
  createdAt: string;
  updatedAt: string;
}

export type SecurityScanType = 'cis-benchmark' | 'psa-audit';

export type SecurityScanStatus = 'pending' | 'running' | 'completed' | 'failed';

export interface SecurityScanCheckResult {
  checkId: string;
  description: string;
  status: 'pass' | 'fail' | 'warn' | 'info';
  severity: 'critical' | 'high' | 'medium' | 'low' | 'info';
  remediation?: string;
  details?: string;
}

export interface SecurityScanSummary {
  total: number;
  passed: number;
  failed: number;
  warned: number;
  info: number;
}

export interface SecurityScanResult {
  id: string;
  cluster: string;
  clusterName: string;
  scanType: SecurityScanType;
  status: SecurityScanStatus;
  summary?: SecurityScanSummary;
  results?: SecurityScanCheckResult[];
  startedAt?: string;
  completedAt?: string;
  initiatedBy: string;
  createdAt: string;
}

// --- Generic K8s Resource ---

export interface GenericK8sResource {
  name: string;
  namespace: string;
  clusterId: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  createdAt: string;
  // Job fields
  completions?: number;
  succeeded?: number;
  failed?: number;
  active?: number;
  status?: string;
  // CronJob fields
  schedule?: string;
  suspend?: boolean;
  lastSchedule?: string;
  activeCount?: number;
  // ConfigMap/Secret fields
  dataCount?: number;
  type?: string;
  // HPA fields
  minReplicas?: number;
  maxReplicas?: number;
  currentReplicas?: number;
  desiredReplicas?: number;
  targetKind?: string;
  targetName?: string;
  // ResourceQuota fields
  hard?: Record<string, string>;
  used?: Record<string, string>;
  // LimitRange fields
  limits?: Array<Record<string, unknown>>;
  // PDB fields
  minAvailable?: string;
  maxUnavailable?: string;
  currentHealthy?: number;
  desiredHealthy?: number;
  disruptionsAllowed?: number;
  // CRD fields
  group?: string;
  kind?: string;
  scope?: string;
  version?: string;
  // ServiceAccount fields
  secretsCount?: number;
  // Role/ClusterRole fields
  rulesCount?: number;
  // RoleBinding/ClusterRoleBinding fields
  roleKind?: string;
  roleName?: string;
  subjectsCount?: number;
  // Endpoints fields
  addressesCount?: number;
  ports?: string;
  // ReplicaSet fields
  desired?: number;
  ready?: number;
  available?: number;
}

// --- Cluster Tools Types ---

export type ToolCategory = 'monitoring' | 'logging' | 'security' | 'backup' | 'mesh';

export interface ClusterTool {
  id: string;
  slug: string;
  name: string;
  description: string;
  icon: string;
  category: ToolCategory;
  charts: Array<{
    chart_name: string;
    repo_url: string;
    namespace: string;
    order: number;
  }>;
  version_constraint: string;
  default_namespace: string;
  is_builtin: boolean;
  is_enabled: boolean;
  presets: Record<string, Record<string, unknown>>;
  service_name: string;
  service_port: number | null;
  service_path: string;
  sub_services: Array<{
    name: string;
    service: string;
    port: number;
  }>;
  form_schema?: ToolFormSchema | null;
}

export interface ToolFormField {
  path: string;
  label: string;
  type: 'string' | 'number' | 'boolean' | 'select' | 'storage';
  group: string;
  default?: string;
  options?: string[];
  help?: string;
  placeholder?: string;
  storage_class_path?: string;
}

export interface ToolFormSchema {
  fields: ToolFormField[];
}

export interface ToolOperationEvent {
  id: string;
  level: string; // info | warn | error
  stage: string;
  message: string;
  detail?: Record<string, unknown> | null;
  createdAt: string;
}

export interface ToolOperation {
  id: string;
  operationType?: string;
  status: string; // pending | running | completed | failed | superseded
  errorMessage?: string;
  startedAt?: string | null;
  completedAt?: string | null;
  createdAt?: string;
  events?: ToolOperationEvent[];
}

export type ToolStatus = 'not_installed' | 'installed' | 'installed_unmanaged' | 'installing' | 'upgrading' | 'failed' | 'uninstalling' | 'unknown';

export interface ClusterToolStatus {
  slug: string;
  name: string;
  status: ToolStatus;
  release_name: string | null;
  namespace: string | null;
  preset_used: string | null;
  error: string | null;
}

export interface ToolPreviewResponse {
  charts: Array<{
    chart_name: string;
    chart_version: string;
    namespace: string;
    values_yaml: string;
  }>;
  preset: string;
}

// --- Live Events (SSE) ---
//
// The backend pushes lifecycle events over `/api/v1/events/stream/`. Type
// names mirror the server-side constants in `internal/events/bus.go`. Page
// hooks should import the strongly-typed wrappers from `lib/live-events.ts`
// rather than handling raw frames here.

export type LiveEventType =
  | 'cluster.connected'
  | 'cluster.disconnected'
  | 'cluster.heartbeat'
  | 'cluster.metrics'
  | 'cluster.status_changed'
  | 'cluster.created'
  | 'cluster.updated'
  | 'cluster.deleted'
  | 'cluster.k8s_changed'
  | 'agent.reconnecting'
  | 'agent.failed';

export interface LiveEvent<T = unknown> {
  id: number;
  type: LiveEventType | string;
  time: string;
  data?: T;
}

export interface LiveClusterMetricsData {
  cluster_id: string;
  cpu_percentage: number;
  memory_percentage: number;
  pod_count: number;
  timestamp: string;
}

export interface LiveClusterHeartbeatData {
  cluster_id: string;
  last_heartbeat: string;
  agent_version?: string;
  agent_build_sha?: string;
  heartbeat_schema_version?: number;
  kubernetes_version?: string;
  node_count?: number;
  pod_count?: number;
  cpu_usage_percent?: number;
  memory_usage_percent?: number;
  distribution?: string;
  privilege_profile?: string;
  available_apis?: string[];
  enabled_features?: string[];
  denied_features?: string[];
  last_successful_action?: string;
  last_successful_action_at?: string;
  degraded_reasons?: string[];
}

export interface LiveClusterStatusChangedData {
  cluster_id: string;
  old_status?: string;
  new_status?: string;
  timestamp?: string;
}

export interface LiveClusterMutationData {
  cluster_id: string;
  name?: string;
  display_name?: string;
  status?: string;
}

export interface LiveAgentLifecycleData {
  cluster_id: string;
  session_id?: string;
  agent_version?: string;
}

// --- Activity Feed ---

export interface ActivityEvent {
  id: string;
  type: 'cluster' | 'workload' | 'deployment' | 'rbac' | 'system';
  action: string;
  message: string;
  user?: string;
  cluster?: string;
  namespace?: string;
  resource?: string;
  timestamp: string;
}

// === Phase B1: ArgoCD lifecycle ===
//
// Wire shapes for the upstream-backed ArgoCD endpoints. The Go backend emits
// a kebab/snake mix, and the axios interceptor camelizes incoming keys, so
// consumer code references camelCase only except for `apiUrl`
// (api_url -> apiUrl), the instance's upstream API endpoint.

/** Augmented row returned by GET /argocd/instances/. */
export interface ArgoInstanceB1 {
  id: string;
  name: string;
  clusterId: string;
  apiUrl: string;
  authToken?: string;
  verifySsl: boolean;
  isHealthy: boolean;
  createdAt: string;
  updatedAt: string;
}

/** Live application row returned by /argocd/instances/{id}/applications/. */
export interface ArgoLiveApplication {
  metadata: {
    name: string;
    namespace?: string;
    uid?: string;
    creationTimestamp?: string;
  };
  spec: {
    project?: string;
    source?: {
      repoURL: string;
      path?: string;
      targetRevision?: string;
      chart?: string;
    };
    destination?: {
      server?: string;
      name?: string;
      namespace?: string;
    };
    syncPolicy?: {
      automated?: { prune?: boolean; selfHeal?: boolean };
      syncOptions?: string[];
    };
  };
  status?: {
    sync?: { status?: string; revision?: string };
    health?: { status?: string };
    operationState?: {
      phase?: string;
      message?: string;
      finishedAt?: string;
    };
  };
}

export interface ArgoCreateApplicationRequest {
  name: string;
  spec: {
    project: string;
    source: {
      repoURL: string;
      path?: string;
      targetRevision?: string;
      chart?: string;
    };
    destination: {
      server?: string;
      name?: string;
      namespace?: string;
    };
    syncPolicy?: {
      automated?: { prune?: boolean; selfHeal?: boolean };
      syncOptions?: string[];
    };
  };
}

export interface ArgoSyncOptions {
  revision?: string;
  prune?: boolean;
  dryRun?: boolean;
  reason?: string;
  syncWindowOverride?: boolean;
}

export interface ArgoAppHistoryEntry {
  id: number;
  revision: string;
  deployedAt?: string;
  deployStartedAt?: string;
  source?: { repoURL: string; targetRevision?: string };
}

export interface ArgoManifests {
  manifests?: string[];
  namespace?: string;
  server?: string;
  revision?: string;
  // Tail of any other fields ArgoCD emits.
  [key: string]: unknown;
}

export interface ArgoProjectSpec {
  description?: string;
  sourceRepos?: string[];
  destinations?: { server?: string; name?: string; namespace?: string }[];
  clusterResourceWhitelist?: { group: string; kind: string }[];
  namespaceResourceWhitelist?: { group: string; kind: string }[];
  syncWindows?: ArgoProjectSyncWindow[];
}

export interface ArgoProjectSyncWindow {
  kind: 'allow' | 'deny' | string;
  schedule: string;
  duration: string;
  applications?: string[];
  namespaces?: string[];
  clusters?: string[];
  manualSync?: boolean;
  syncOverrun?: boolean;
  timeZone?: string;
  useAndOperator?: boolean;
  description?: string;
}

export interface ArgoProject {
  metadata: { name: string; namespace?: string };
  spec: ArgoProjectSpec;
}

export interface ArgoCreateProjectRequest {
  name: string;
  spec: ArgoProjectSpec;
}

export interface ArgoApplicationSetGenerator {
  list?: { elements: Record<string, string>[] };
  clusters?: {
    selector?: {
      matchLabels?: Record<string, string>;
      matchExpressions?: { key: string; operator: string; values?: string[] }[];
    };
    values?: Record<string, string>;
  };
  git?: {
    repoURL: string;
    revision?: string;
    files?: { path: string }[];
    directories?: { path: string; exclude?: boolean }[];
  };
}

export interface ArgoApplicationSetSpec {
  generators: ArgoApplicationSetGenerator[];
  template: {
    metadata: { name: string; namespace?: string; labels?: Record<string, string> };
    spec: {
      project: string;
      source: { repoURL: string; path?: string; targetRevision?: string; chart?: string };
      destination: { server?: string; name?: string; namespace?: string };
      syncPolicy?: { automated?: { prune?: boolean; selfHeal?: boolean }; syncOptions?: string[] };
    };
  };
  syncPolicy?: { preserveResourcesOnDeletion?: boolean };
}

export interface ArgoApplicationSet {
  metadata: { name: string; namespace?: string };
  spec: ArgoApplicationSetSpec;
}

export interface ArgoCreateApplicationSetRequest {
  name: string;
  spec: ArgoApplicationSetSpec;
}

/** A managed cluster row registered into a particular ArgoCD instance. */
export interface ArgoManagedCluster {
  id: string;
  argocdInstanceId: string;
  clusterId: string;
  server: string;
  clusterSecretName?: string;
  labels?: Record<string, string>;
  createdAt: string;
}

export interface ArgoOrphanApplication {
  id?: string;
  name: string;
  componentSlug?: string;
  applicationSetName?: string;
  destinationCluster: string;
  destinationNamespace?: string;
  reason:
    | 'missing_destination'
    | 'stale_destination_cluster'
    | 'live_missing_destination'
    | 'live_stale_destination_cluster'
    | 'stale_applicationset_metadata'
    | string;
  source: 'cache' | 'live' | string;
  message: string;
}

export interface ArgoOrphanReport {
  instanceId: string;
  applicationCount: number;
  cachedApplicationCount: number;
  liveApplicationCount: number;
  managedTargetCount: number;
  orphanApplicationCount: number;
  orphanApplications: ArgoOrphanApplication[];
  liveError?: string;
  generatedAt: string;
}

export interface ArgoManagedClusterRegisterRequest {
  bearer_token?: string;
  ca_data?: string;
  insecure?: boolean;
  labels?: Record<string, string>;
  project?: string;
  namespaces?: string[];
  server?: string;
  name?: string;
}

export interface ArgoRepository {
  repo: string;
  name?: string;
  type?: 'git' | 'helm' | string;
  username?: string;
  insecure?: boolean;
  enableLfs?: boolean;
  project?: string;
  connectionState?: { status?: string; message?: string; attemptedAt?: string };
}

export interface ArgoRepositoryCreate {
  repo: string;
  type?: 'git' | 'helm';
  name?: string;
  username?: string;
  password?: string;
  ssh_private_key?: string;
  insecure?: boolean;
  enable_lfs?: boolean;
  project?: string;
}

/** Operation row returned by /argocd/operations/. */
export interface ArgoOperation {
  id: string;
  targetType: string;
  targetKey: string;
  operationType: string;
  status: 'pending' | 'running' | 'completed' | 'failed' | 'superseded' | string;
  attemptCount: number;
  startedAt?: string | null;
  completedAt?: string | null;
  errorMessage?: string;
  createdAt: string;
  updatedAt: string;
  // Returned only on the detail endpoint.
  events?: ArgoOperationEvent[];
}

export interface ArgoOperationEvent {
  id: string;
  level: string;
  stage: string;
  message: string;
  detail?: Record<string, unknown>;
  createdAt: string;
}

// === Phase B4: Dex ===
//
// Mirrors the JSON shapes returned by /api/v1/auth/dex/* in
// astronomer-go/internal/handler/dex_config.go. The Go handler emits snake_case;
// the axios response interceptor camelizes everything before it reaches us, so
// these interfaces are camelCase. Dex connector `config` payloads are kept as
// `Record<string, unknown>` because the schema is per-connector-type and lives
// in `DexConnectorTypeSpec.required` / `optional`.

/** Connector type registry entry. GET /api/v1/auth/dex/connector-types/ */
export interface DexConnectorTypeSpec {
  type: string;
  displayHint: string;
  required: string[];
  optional: string[];
  /** Field names whose values are sensitive. The wizard renders these as
   *  password inputs; the API redacts them and sets `__<name>_set` on read. */
  secret: string[];
  /** Required nested fields, e.g. `userSearch.{baseDN,...}` for ldap. */
  nested: Array<{ parent: string; keys: string[] }>;
}

/** Configured connector row. GET /api/v1/auth/dex/connectors/ */
export interface DexConnector {
  id: string;
  name: string;
  type: string;
  displayName: string;
  enabled: boolean;
  /** Validated against the type's spec. Secret values come back as empty
   *  strings with a sibling `__<key>_set: true` flag so the UI can show
   *  "(set)" placeholders without leaking ciphertext. */
  config: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}

/** Body for POST /connectors/ and PATCH /connectors/{id}/ */
export interface DexConnectorWriteRequest {
  type: string;
  name: string;
  displayName: string;
  config: Record<string, unknown>;
  enabled?: boolean;
}

/** Public client entry under DexSettings.publicClients. */
export interface DexPublicClient {
  id: string;
  name?: string;
  redirectURIs?: string[];
  secret?: string;
  public?: boolean;
}

/** Singleton settings. GET / PUT /api/v1/auth/dex/settings/ */
export interface DexSettings {
  issuerUrl: string;
  clusterId: string;
  namespace: string;
  releaseName: string;
  configmapName: string;
  publicClients: DexPublicClient[];
  expiry: Record<string, unknown>;
  extra: Record<string, unknown>;
  configured: boolean;
  updatedAt?: string;
}

/** Body for PUT /settings/. snake_case to match the Go handler exactly —
 *  the request interceptor does not transform outbound bodies. */
export interface DexSettingsWriteRequest {
  issuer_url: string;
  cluster_id?: string;
  namespace?: string;
  release_name?: string;
  configmap_name?: string;
  public_clients?: DexPublicClient[];
  expiry?: Record<string, unknown>;
  extra?: Record<string, unknown>;
}

/** Response from POST /apply/ */
export interface DexApplyResponse {
  applied: boolean;
  clusterId: string;
  namespace: string;
  configmapName: string;
  connectorCount: number;
  appliedAt: string;
}

/** Body for POST /register-as-sso/ */
export interface DexRegisterAsSSORequest {
  client_id?: string;
  client_secret?: string;
  display_name?: string;
}

/** Response from POST /register-as-sso/ */
export interface DexRegisterAsSSOResponse {
  provider: string;
  id: string;
  isEnabled: boolean;
  clientId: string;
  issuerUrl: string;
  displayName: string;
  created?: boolean;
  updated?: boolean;
}

// === Phase B2: Velero backups ===
//
// Wire shapes for the Velero-backed endpoints under `/api/v1/backups/`. The
// Go handler emits snake_case JSON, and the axios interceptor in
// `lib/api.ts::camelizeKeys` rewrites incoming keys to camelCase, so the
// read-side shapes below use camelCase. Request bodies stay snake_case to
// match the Go `json:` tags exactly.

export type VeleroPhase =
  | 'New'
  | 'InProgress'
  | 'Completed'
  | 'PartiallyFailed'
  | 'Failed'
  | 'FailedValidation'
  | 'Deleting'
  | 'Finalizing'
  | string;

/** Storage location row as returned by `/backups/storage/`. Mirrors
 *  `BackupHandler.storageResponse` — credentials never round-trip; only the
 *  `hasCredentials` flag indicates whether a Fernet-sealed secret is on
 *  file. */
export interface BackupStorageLocation {
  id: string;
  name: string;
  storageType: BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpointUrl?: string;
  isDefault: boolean;
  veleroNamespace?: string;
  bslName?: string;
  hasCredentials: boolean;
  clusterId?: string;
  createdAt: string;
  updatedAt: string;
}

/** Schedule row as returned by `/backups/schedules/`. Included/excluded
 *  namespace columns are stored as JSON in postgres and arrive as parsed
 *  arrays after the camelize pass. */
export interface BackupScheduleRow {
  id: string;
  name: string;
  storageId: string;
  backupType: BackupType;
  cronExpression: string;
  retentionCount: number;
  enabled: boolean;
  lastBackupId?: string;
  clusterId?: string;
  veleroNamespace?: string;
  veleroScheduleName?: string;
  includedNamespaces?: string[] | null;
  excludedNamespaces?: string[] | null;
  ttl?: string;
  createdAt: string;
  updatedAt: string;
}

/** A backup run (Velero `Backup` CR projection). Item counts and phase are
 *  populated by the controller-side reconciler — they may be undefined while
 *  the run is still being scheduled. */
export interface BackupRun {
  id: string;
  name: string;
  storageId: string;
  backupType: BackupType;
  status: BackupStatus;
  filePath?: string;
  fileSizeBytes?: number;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
  clusterId?: string;
  veleroBackupName?: string;
  veleroNamespace?: string;
  includedNamespaces?: string[] | null;
  excludedNamespaces?: string[] | null;
  pollAttempts?: number;
  lastPolledAt?: string;
  createdById?: string;
  createdAt: string;
  updatedAt: string;
  // Optional decorators the reconciler may project once known.
  phase?: VeleroPhase;
  itemsBackedUp?: number;
  totalItems?: number;
  warnings?: number;
  errors?: number;
}

export interface BackupRestore {
  id: string;
  backupId: string;
  status: BackupStatus;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
  clusterId?: string;
  veleroNamespace?: string;
  veleroRestoreName?: string;
  includedNamespaces?: string[] | null;
  namespaceMapping?: Record<string, string> | null;
  pollAttempts?: number;
  lastPolledAt?: string;
  createdAt: string;
  updatedAt: string;
  phase?: VeleroPhase;
  itemsRestored?: number;
  warnings?: number;
  errors?: number;
}

/** Wizard-only union. `s3-compatible` is a UI alias for the AWS plugin
 *  driving an arbitrary S3 endpoint (MinIO, Cloudflare R2, etc.); on the
 *  wire it serializes as `s3` with a populated `endpoint_url`. */
export type BackupBackendKind = 's3' | 'gcs' | 'azure' | 's3-compatible';

export interface CreateBackupStorageRequest {
  name: string;
  cluster_id?: string;
  storage_type: BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpoint_url?: string;
  access_key?: string;
  secret_key?: string;
  is_default?: boolean;
}

export interface TestStorageResult {
  success: boolean;
  message: string;
}

export interface CreateScheduleRequestB2 {
  name: string;
  storage_id: string;
  backup_type?: BackupType;
  cron_expression: string;
  included_namespaces?: string[];
  excluded_namespaces?: string[];
  ttl?: string;
  retention_count: number;
  enabled: boolean;
  cluster_id?: string;
}

export interface CreateRestoreRequestB2 {
  backup_id: string;
  included_namespaces?: string[];
  namespace_mapping?: Record<string, string>;
  restore_pvs?: boolean;
}

// === Phase B5: CIS ===
//
// CIS scan types backed by `internal/handler/security.go`. The Go handler
// emits snake_case which the axios interceptor camelizes on the way in, so
// these mirror the wire shape after that transform.

export type CISScanStatus = 'pending' | 'running' | 'completed' | 'failed' | string;

export type CISFindingSeverity = 'critical' | 'high' | 'medium' | 'low' | 'info' | string;

export type CISFindingStatus = 'pass' | 'fail' | 'warn' | 'skip' | 'info' | string;

/**
 * One row out of a `ClusterScanReport` after we've flattened it into the
 * cis-operator-agnostic finding shape the backend persists in JSONB.
 */
export interface CISFinding {
  testId: string;
  severity: CISFindingSeverity;
  status: CISFindingStatus;
  description: string;
  remediation: string;
}

/** ClusterScanProfile entry returned by `/security/profiles/?cluster_id=X`. */
export interface CISProfile {
  name: string;
  benchmarkVersion: string;
}

/** Full envelope so callers can distinguish operator-installed vs fallback. */
export interface CISProfilesResponse {
  items: CISProfile[];
  /** `cluster` if the cis-operator was queried, `fallback` otherwise. */
  source: 'cluster' | 'fallback';
  /** Populated when the cluster query failed and we returned the static set. */
  error?: string;
}

/**
 * Paginated list row. Findings are not included to keep the payload light;
 * fetch `getCISScan` for the full breakdown.
 */
export interface CISScanListItem {
  id: string;
  clusterId: string;
  scanType: string;
  status: CISScanStatus;
  passed: number;
  failed: number;
  warned: number;
  skipped: number;
  startedAt?: string;
  completedAt?: string | null;
  clusterScanName?: string;
  initiatedById?: string;
  errorMessage?: string;
  createdAt: string;
  updatedAt: string;
}

/** Full scan response from `GET /security/scans/{id}/`. */
export interface CISScanDetail extends CISScanListItem {
  findings: CISFinding[];
  /** Raw JSON from cis-operator — opaque to us; surfaced for debugging. */
  summary?: unknown;
  results?: unknown;
}

/** Body for `POST /security/scans/`. */
export interface CISScanCreatePayload {
  cluster_id: string;
  /** Defaults from `cluster.distribution` when omitted. */
  profile?: string;
}
