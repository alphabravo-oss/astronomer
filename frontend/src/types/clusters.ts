import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// --- Cluster Types ---

export type ClusterStatus =
  | "pending"
  | "active"
  | "connecting"
  | "warning"
  | "error"
  | "disconnected"
  | "provisioning";

export type ClusterProvider =
  | "aws"
  | "gcp"
  | "azure"
  | "eks"
  | "gke"
  | "aks"
  | "doks"
  | "self-managed"
  | "on-prem"
  | "digitalocean"
  | "other"
  | string;

export type ClusterEnvironment =
  "production" | "staging" | "development" | "dev" | "testing" | string;

export interface ClusterHealth {
  status: ClusterStatus;
  message?: string;
  lastCheck: string;
  components: ClusterHealthComponent[];
}

export interface ClusterHealthComponent {
  name: string;
  status: "healthy" | "degraded" | "unhealthy" | "unknown";
  message?: string;
}

export type ClusterDistribution =
  "k3s" | "rke2" | "eks" | "aks" | "gke" | "openshift" | "k8s" | "";

/**
 * Explicit view model for the cluster wire DTO. The optional health/capacity
 * fields are UI enrichments from separate metrics/conditions APIs; they are
 * no longer falsely presented as fields returned by cluster CRUD.
 */
export type Cluster = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["Cluster"]>,
  | "status"
  | "provider"
  | "environment"
  | "distribution"
  | "agentPrivilegeProfile"
> & {
  status: ClusterStatus;
  provider: ClusterProvider;
  environment: ClusterEnvironment;
  distribution: ClusterDistribution;
  agentPrivilegeProfile:
    | "viewer"
    | "operator"
    | "namespace-viewer"
    | "namespace-operator"
    | "custom"
    | "admin"
    | string;
  health?: ClusterHealth;
  namespaceCount?: number;
  cpuCapacity?: number;
  cpuUsage?: number;
  memoryCapacity?: number;
  memoryUsage?: number;
};

export type ClusterAgentStatus = "connected" | "degraded" | "disconnected";

export type AgentOfflineBehavior = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["AgentOfflineBehavior"]>,
  | "state"
  | "stale"
  | "message"
  | "permittedQueuedOperations"
  | "blockedOperations"
> & {
  state: "offline" | string;
  lastKnownAt?: string;
  stale: boolean;
  message: string;
  permittedQueuedOperations: string[];
  blockedOperations: string[];
};

export interface ClusterAgentSummary {
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

export type ClusterAgentItem = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterAgentItem"]>,
  | "clusterId"
  | "clusterName"
  | "clusterDisplayName"
  | "clusterStatus"
  | "isLocal"
  | "agentStatus"
  | "nodeCount"
  | "privilegeProfile"
  | "capabilities"
  | "compatibilityStatus"
  | "offlineBehavior"
> & {
  clusterId: string;
  clusterName: string;
  clusterDisplayName: string;
  clusterStatus: string;
  isLocal: boolean;
  agentStatus: ClusterAgentStatus;
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
  privilegeProfile: "viewer" | "operator" | "admin" | string;
  capabilities: Record<string, boolean>;
  compatibilityStatus:
    "supported" | "deprecated" | "blocked" | "unknown" | string;
  compatibilityMessage?: string;
  degradedReasons?: string[];
  recommendedAction?: string;
  offlineBehavior?: AgentOfflineBehavior;
};

export type ClusterAgentResponse = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterAgentResponse"]>,
  "summary" | "items" | "limit" | "offset"
> & {
  summary: ClusterAgentSummary;
  items: ClusterAgentItem[];
  limit: number;
  offset: number;
};

export interface AgentConnectionDiagnosticView {
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

export interface AgentClusterConditionDiagnosticView {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime: string;
  lastProbeTime?: string;
}

export interface AgentUpgradeRecommendationView {
  currentVersion?: string;
  status: string;
  message: string;
}

export interface AgentLiveDiagnosticCheck {
  name: string;
  status: "passed" | "warning" | "failed" | string;
  message: string;
}

export interface AgentLivePodDiagnosticView {
  name: string;
  namespace: string;
  phase: string;
  nodeName?: string;
  ready: boolean;
  restartCount: number;
  containerImages?: string[];
}

export interface AgentLiveDiagnosticsView {
  collectedAt: string;
  deployment?: Record<string, unknown>;
  pods?: AgentLivePodDiagnosticView[];
  events?: Array<Record<string, unknown>>;
  logs?: Array<Record<string, unknown>>;
  discovery?: Record<string, unknown>;
  checks?: AgentLiveDiagnosticCheck[];
  errors?: string[];
}

export interface AgentDiagnosticsResponse {
  generatedAt: string;
  agent: ClusterAgentItem;
  recentConnections: AgentConnectionDiagnosticView[];
  conditions: AgentClusterConditionDiagnosticView[];
  live?: AgentLiveDiagnosticsView;
  recommendations: string[];
  redactions: string[];
  upgradeRecommendation: AgentUpgradeRecommendationView;
}

export type AgentSelfTestStatus = "passed" | "warning" | "failed" | string;

export interface AgentSelfTestCheckView {
  name: string;
  status: AgentSelfTestStatus;
  message: string;
}

export interface AgentSelfTestResponse {
  generatedAt: string;
  clusterId: string;
  clusterName: string;
  status: AgentSelfTestStatus;
  checks: AgentSelfTestCheckView[];
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
  operationType: "agent_upgrade" | string;
  status: "pending" | "running" | "succeeded" | "failed" | "cancelled" | string;
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

// metav1.Condition-shaped row written by the health-check worker
// (internal/worker/tasks/health_check.go). Types currently emitted:
//   Connected           — agent heartbeat freshness
//   AgentReachable      — GET /version round-trip through the tunnel
//   GatewayAPISupported — gateway.networking.k8s.io/v1 discovery probe
export interface ClusterCondition {
  type: string;
  status: "True" | "False" | "Unknown";
  reason: string;
  message: string;
  last_transition_time: string;
  last_probe_time: string;
}

// Sprint 086 — entries the cluster-condition remediation reconciler
// writes when it attempts to drive a False condition back to True.
// Shown in the cluster header as "Last action: $action — $outcome".
export interface ClusterConditionRemediationAttemptView {
  id: string;
  cluster_id: string;
  condition_type: string;
  action: string;
  outcome: "success" | "failed" | "skipped";
  error: string | null;
  detail: Record<string, unknown> | null;
  attempted_at: string;
}

export interface ClusterNode {
  name: string;
  status: "Ready" | "NotReady" | "SchedulingDisabled";
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
  // Same closed enum the taint write path uses, so a taint read off a node can
  // be handed straight back to removeNodeTaint without a cast.
  effect: OpenAPIComponents["schemas"]["NodeTaintRequest"]["effect"];
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
  status: "Ready" | "NotReady" | "SchedulingDisabled";
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
  type: "Normal" | "Warning";
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
  // Sprint 078 — registration wizard fields. distribution is a free-form
  // dropdown on the wizard; the backend accepts it but doesn't otherwise
  // gate on it. install_baseline is the Quick Start opt-in.
  distribution?: ClusterDistribution;
  region?: string;
  provider?: ClusterProvider;
  install_baseline?: boolean;
  // Free-form cluster annotations passed to the backend at create time. The
  // wizard uses this to set astronomer.io/agent-privilege-profile so the
  // rendered agent manifest scopes the agent's RBAC (defaults to viewer).
  annotations?: Record<string, string>;
  /** Optional externally reachable Kubernetes API origin for 15m direct access. */
  apiServerUrl?: string;
  /** Optional PEM CA bundle for the direct Kubernetes API origin. */
  caCertificate?: string;
}
