import api from "@/lib/api";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { AssertNoPhantomWireKeys } from "@/types/wire-contract";

type DeliveryContracts = OpenAPIComponents["schemas"];

/**
 * Flux-native delivery wire contract.
 *
 * Request fields deliberately retain their documented snake_case spelling.
 * Axios' response interceptor converts management-plane response keys to
 * camelCase, so response types below use camelCase. Secret material exists
 * only on create/rotate request types and is never represented by a response
 * type.
 */

export type DeliverySourceType =
  "git" | "oci_artifact" | "helm_http" | "helm_oci";
export type DeliveryAuthMode =
  "none" | "basic" | "bearer" | "ssh" | "workload_identity";
export type SignatureProvider = "cosign_key" | "cosign_keyless" | "git";
export type RendererKind = "helm" | "kustomize";
export type BundleScope = "namespace" | "platform";
export type DriftPolicy = "ignore" | "detect" | "repair";
export type LabelOperator = "In" | "NotIn" | "Exists" | "DoesNotExist";
export type RolloutStrategyType =
  "all_at_once" | "rolling" | "canary" | "partitioned";
export type RolloutFailureAction = "pause" | "abort" | "rollback";
export type AmountType = "count" | "percent";

export type RolloutState =
  | "draft"
  | "resolving"
  | "awaiting_approval"
  | "rejected"
  | "queued"
  | "progressing"
  | "paused"
  | "aborted"
  | "succeeded"
  | "failed"
  | "rolling_back"
  | "rolled_back"
  | "rollback_failed";

export type RolloutClusterState =
  | "pending"
  | "released"
  | "acknowledged"
  | "reconciling"
  | "ready"
  | "blocked"
  | "timed_out"
  | "failed"
  | "skipped"
  | "rolling_back"
  | "ready_previous"
  | "rollback_failed"
  | "aborted";

export type DeploymentPhase =
  | "pending"
  | "blocked"
  | "applying"
  | "ready"
  | "degraded"
  | "failed"
  | "suspended"
  | "deleting"
  | "removed"
  | "unknown";

export interface DeliveryPage<T> {
  data: T[];
  count: number;
  next: string | null;
  previous: string | null;
  totalKnown: boolean;
}

export interface EntityResponse<T> {
  data: T;
  etag?: string;
}

interface DataEnvelope<T> {
  data: T;
}

export interface TrustPolicy {
  allowUnsigned: boolean;
  provider?: SignatureProvider;
  identity?: string;
  issuer?: string;
  keyRef?: string;
}

export interface TrustPolicyRequest {
  allow_unsigned: boolean;
  provider?: SignatureProvider;
  identity?: string;
  issuer?: string;
  key_ref?: string;
}

export interface SourceCredentialInput {
  username?: string;
  password?: string;
  token?: string;
  private_key?: string;
  known_hosts?: string;
  passphrase?: string;
}

export interface DeliverySource {
  id: string;
  projectId: string;
  name: string;
  description?: string;
  type: DeliverySourceType;
  url: string;
  authMode: DeliveryAuthMode;
  credential: { configured: boolean; keyVersion: number; epoch: number };
  proxyRef?: string;
  trustPolicy: TrustPolicy;
  status: string;
  lastResolvedAt: string | null;
  lastErrorCode?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateDeliverySourceRequest {
  project_id: string;
  name: string;
  description?: string;
  type: DeliverySourceType;
  url: string;
  auth_mode: DeliveryAuthMode;
  credential?: SourceCredentialInput;
  ca_bundle?: string;
  proxy_ref?: string;
  trust_policy: TrustPolicyRequest;
}

export interface RotateSourceCredentialRequest {
  project_id: string;
  auth_mode: DeliveryAuthMode;
  credential: SourceCredentialInput;
}

export interface SourceVerification {
  id: string;
  sourceId: string;
  status: string;
}

export interface ComponentBundle {
  id: string;
  projectId: string;
  name: string;
  description?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CapabilityRequirement {
  name: string;
  constraint?: string;
}

export interface ReconciliationPolicy {
  interval: string;
  retryInterval: string;
  timeout: string;
  prune: boolean;
  wait: boolean;
  drift: DriftPolicy;
}

export interface KustomizeRenderer {
  path: string;
  targetNamespace: string;
  patches?: string[];
}

export interface HelmRenderer {
  chart: string;
  chartVersion: string;
  releaseName: string;
  targetNamespace: string;
  values?: Record<string, unknown>;
  installRetries: number;
  upgradeRetries: number;
  test: boolean;
}

export interface RendererSpec {
  kind: RendererKind;
  kustomize?: KustomizeRenderer;
  helm?: HelmRenderer;
}

export interface ComponentBundleVersion {
  id: string;
  bundleId: string;
  sourceId: string;
  version: string;
  renderer: RendererKind;
  scope: BundleScope;
  requestedRevision: string;
  resolvedRevision?: string;
  artifactDigest?: string;
  rendererSpec: RendererSpec;
  reconciliationPolicy: ReconciliationPolicy;
  requiredCapabilities: CapabilityRequirement[];
  dependencyBundleIds: string[];
  specDigest: string;
  verificationStatus: string;
  verificationIdentity?: string;
  state: string;
  lastErrorCode?: string;
  createdAt: string;
}

export interface RendererSpecRequest {
  kind: RendererKind;
  kustomize?: {
    path: string;
    target_namespace: string;
    patches?: string[];
  };
  helm?: {
    chart: string;
    chart_version: string;
    release_name: string;
    target_namespace: string;
    values?: Record<string, unknown>;
    install_retries: number;
    upgrade_retries: number;
    test: boolean;
  };
}

export interface CreateBundleVersionRequest {
  project_id: string;
  version: string;
  spec: {
    source_id: string;
    requested_revision: string;
    renderer: RendererSpecRequest;
    scope: BundleScope;
    reconciliation_policy: {
      interval: string;
      retry_interval: string;
      timeout: string;
      prune: boolean;
      wait: boolean;
      drift: DriftPolicy;
    };
    required_capabilities?: Array<{ name: string; constraint?: string }>;
  };
  dependency_bundle_ids?: string[];
}

export interface LabelExpression {
  key: string;
  operator: LabelOperator;
  values?: string[];
}

export interface Placement {
  projectIds?: string[];
  clusterIds?: string[];
  clusterGroupIds?: string[];
  matchLabels?: Record<string, string>;
  matchExpressions?: LabelExpression[];
  excludeClusterIds?: string[];
  allClusters: boolean;
}

export interface PlacementRequest extends Record<string, unknown> {
  project_ids?: string[];
  cluster_ids?: string[];
  cluster_group_ids?: string[];
  match_labels?: Record<string, string>;
  match_expressions?: Array<{
    key: string;
    operator: LabelOperator;
    values?: string[];
  }>;
  exclude_cluster_ids?: string[];
  all_clusters: boolean;
}

export interface DeliveryTarget {
  id: string;
  projectId: string;
  name: string;
  description?: string;
  bundleVersionId: string;
  placement: Placement;
  rolloutPolicy: { approvalRequired: boolean };
  reconciliationPolicy: ReconciliationPolicy;
  maintenanceWindowPolicy: Record<string, unknown>;
  suspended: boolean;
  generation: number;
  resourceVersion: number;
  deletionState: string;
  createdAt: string;
  updatedAt: string;
}

export interface DeliveryTargetRequest {
  project_id: string;
  name: string;
  description?: string;
  bundle_version_id: string;
  placement: PlacementRequest;
  rollout_policy: { approval_required: boolean };
  reconciliation_policy: {
    [key: string]: unknown;
    interval: string;
    retry_interval: string;
    timeout: string;
    prune: boolean;
    wait: boolean;
    drift: DriftPolicy;
  };
  maintenance_window_policy?: Record<string, unknown>;
  suspended: boolean;
}

export type PlacementDecisionReason =
  | "selected"
  | "excluded_by_selector"
  | "excluded_explicitly"
  | "unauthorized"
  | "disconnected"
  | "incompatible"
  | "missing_capability"
  | "decommissioning";

export interface PlacementDecision {
  clusterId: string;
  projectId?: string;
  clusterName?: string;
  reason: PlacementDecisionReason;
  matchReasons?: Array<
    | "explicit_cluster"
    | "all_clusters"
    | "cluster_group"
    | "match_labels"
    | "match_expressions"
  >;
  matchedGroupIds?: string[];
  missingCapabilities?: string[];
  compatibilityReason?: string;
}

export interface PlacementPreview {
  targetId: string;
  targetGeneration: number;
  bundleVersionId: string;
  previewDigest: string;
  selectedCount: number;
  excludedCount: number;
  requiresAllConfirmation: boolean;
  decisions: PlacementDecision[];
  decisionCount: number;
  decisionOffset: number;
  decisionPageSize: number;
  hasMoreDecisions: boolean;
  nextCursor: string;
  risks: string[];
}

export interface PlacementPreviewPageParams {
  pageSize?: number;
  cursor?: string;
}

export interface Amount {
  type: AmountType;
  value: number;
}

export interface RolloutStrategy {
  type: RolloutStrategyType;
  maxConcurrent: number;
  maxUnavailable: Amount;
  minReady: string;
  progressDeadline: string;
  failureThreshold: Amount;
  onFailure: RolloutFailureAction;
  respectMaintenanceWindows: boolean;
  shuffleSeed?: string;
  canary?: {
    size: Amount;
    clusterIds?: string[];
    approvalAfterCanary: boolean;
    soak: string;
  };
  partitions?: Array<{
    name: string;
    selector: Placement;
    approvalRequired: boolean;
    soak: string;
  }>;
}

export interface RolloutStrategyRequest extends Record<string, unknown> {
  type: RolloutStrategyType;
  max_concurrent: number;
  max_unavailable: Amount;
  min_ready: string;
  progress_deadline: string;
  failure_threshold: Amount;
  on_failure: RolloutFailureAction;
  respect_maintenance_windows: boolean;
  shuffle_seed?: string;
  canary?: {
    size: Amount;
    cluster_ids?: string[];
    approval_after_canary: boolean;
    soak: string;
  };
  partitions?: Array<{
    name: string;
    selector: PlacementRequest;
    approval_required: boolean;
    soak: string;
  }>;
}

export interface DeliveryRollout {
  id: string;
  targetId: string;
  targetGeneration: number;
  fromBundleVersionId?: string;
  toBundleVersionId: string;
  placementDigest: string;
  strategy: RolloutStrategy;
  planDigest: string;
  state: RolloutState;
  fencingGeneration: number;
  totalClusters: number;
  readyClusters: number;
  failedClusters: number;
  blockedClusters: number;
  releasedClusters: number;
  progressDeadline?: string;
  startedAt?: string;
  completedAt?: string;
  lastErrorCode?: string;
  createdAt: string;
  updatedAt: string;
}

export interface DeliveryRolloutApproval {
  id: string;
  rolloutId: string;
  cohort: number;
  bindingDigest: string;
  decision: "approved" | "rejected";
  decidedBy?: string;
  decidedAt: string;
  expiresAt: string;
  createdAt: string;
}

export interface DeliveryRolloutEvent {
  id: string;
  rolloutId: string;
  clusterId?: string;
  decisionDigest: string;
  eventType: string;
  fromState?: string;
  toState?: string;
  reasonCode?: string;
  fence: number;
  occurredAt: string;
  createdAt: string;
}

export interface DeliveryRolloutDetail {
  rollout: DeliveryRollout;
  frozenPlan: {
    id: string;
    targetId: string;
    projectId: string;
    targetGeneration: number;
    desired: {
      bundleVersionId: string;
      specDigest: string;
      source: {
        sourceId: string;
        type: DeliverySourceType;
        url: string;
        authMode: DeliveryAuthMode;
        trust: TrustPolicy;
        revision: { kind: string; value: string; artifactDigest: string };
      };
    };
    placementDigest: string;
    strategy: RolloutStrategy;
    strategyDigest: string;
    approval: { required: boolean; digest: string };
    actor: string;
    requestDigest: string;
    createdAt: string;
    deadline: string;
    cohorts: Array<{
      index: number;
      name: string;
      clusterIds: string[];
      approvalRequired: boolean;
      approvalDigest?: string;
      soakAfter: string;
    }>;
    planDigest: string;
  };
  approvals: DeliveryRolloutApproval[];
  timeline: DeliveryRolloutEvent[];
}

export interface DeliveryFrozenRollout {
  id: string;
  targetId: string;
  projectId: string;
  targetGeneration: number;
  desired: DeliveryRolloutDetail["frozenPlan"]["desired"];
  placementDigest: string;
  strategy: RolloutStrategy;
  strategyDigest: string;
  approval: { required: boolean; digest: string };
  actor: string;
  idempotencyKey: string;
  requestDigest: string;
  createdAt: string;
  deadline: string;
  cohorts: DeliveryRolloutDetail["frozenPlan"]["cohorts"];
  clusters: Array<{
    clusterId: string;
    cohort: number;
    order: number;
    previous?: Record<string, unknown>;
  }>;
  planDigest: string;
}

export interface DeliveryRolloutCluster {
  id: string;
  rolloutId: string;
  clusterId: string;
  cohort: number;
  releaseOrder: number;
  previousBundleVersionId?: string;
  desiredBundleVersionId: string;
  desiredSpecDigest: string;
  state: RolloutClusterState;
  assignmentAction: "apply" | "rollback";
  attempt: number;
  fence: number;
  releasedAt?: string;
  acknowledgedAt?: string;
  readyAt?: string;
  completedAt?: string;
  deadline?: string;
  lastErrorCode?: string;
  createdAt: string;
  updatedAt: string;
}

export interface DeliveryCondition {
  type: "Ready" | "Reconciling" | "Stalled" | "Drifted";
  status: "True" | "False" | "Unknown";
  reason?: string;
  message?: string;
  observedGeneration: number;
  lastTransitionTime: string;
}

export interface ClusterDeployment {
  id: string;
  targetId: string;
  clusterId: string;
  currentRolloutId?: string;
  desiredBundleVersionId?: string;
  previousBundleVersionId?: string;
  desiredGeneration: number;
  observedGeneration: number;
  desiredSpecDigest: string;
  observedSpecDigest: string;
  desiredRevision: string;
  observedRevision: string;
  action: "apply" | "suspend" | "delete";
  phase: DeploymentPhase;
  conditions: DeliveryCondition[];
  sourceKind: string;
  sourceName: string;
  reconcilerKind: string;
  reconcilerName: string;
  inventory: Record<string, unknown>;
  agentSessionId: string;
  agentSequence: number;
  lastErrorCode: string;
  lastMessage: string;
  lastObservedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ClusterDeploymentEvent {
  id: string;
  deploymentId: string;
  rolloutId?: string;
  eventType: string;
  fromPhase?: string;
  toPhase?: string;
  generation: number;
  specDigest?: string;
  reasonCode?: string;
  message?: string;
  observedAt: string;
  createdAt: string;
}

export interface ClusterDeploymentDetail {
  deployment: ClusterDeployment;
  events: ClusterDeploymentEvent[];
}

export interface DeliveryControllerInventory {
  clusterId: string;
  agentVersion: string;
  fluxVersion: string;
  components: Record<string, string>;
  apiVersions: string[];
  distributionDigest: string;
  kubernetesVersion: string;
  ready: boolean;
  compatibilityStatus: string;
  errorCode: string;
  observedAt?: string;
  updatedAt: string;
}

export interface ClusterDeliveryInventory {
  controllerInventory: DeliveryControllerInventory;
  deployments: ClusterDeployment[];
  deploymentCount: number;
}

export interface DeliverySystemCompatibility {
  contract: {
    summary: string;
    fluxVersion: string;
    fluxComponents: Record<string, string>;
    fluxApis: string[];
    kubernetesMinimum: string;
    kubernetesMaximum: string;
    agentProtocol: string;
    requiredCapabilities: string[];
  };
  currentRelease: Record<string, unknown> | null;
  currentRollout: Record<string, unknown> | null;
  observedInventory: Array<{
    compatibilityStatus: string;
    clusterCount: number;
  }>;
}

export interface DeliveryFleetCount {
  key: string;
  count: number;
}

export interface DeliveryFleetSummary {
  adoptedClusters: number;
  fluxReady: number;
  incompatible: number;
  disconnected: number;
  stale: number;
  assignments: number;
  drifted: number;
  failed: number;
  degraded: number;
  activeRollouts: number;
}

export interface DeliveryFleetCluster {
  id: string;
  name: string;
  displayName: string;
  isLocal: boolean;
  connected: boolean;
  stale: boolean;
  privilegeProfile: string;
  kubernetesVersion: string;
  agentVersion: string;
  fluxVersion: string;
  compatibilityStatus: string;
  inventoryReady: boolean;
  inventoryErrorCode: string;
  assignmentCount: number;
  readyCount: number;
  failedCount: number;
  degradedCount: number;
  driftedCount: number;
  lastHeartbeat: string | null;
  inventoryObservedAt: string | null;
  lastObservedAt: string | null;
}

export interface DeliveryFleetAttention {
  clusterId: string;
  clusterName: string;
  severity: "error" | "warning";
  reason: string;
  detail: string;
}

export interface DeliveryFleetDistributions {
  compatibility: DeliveryFleetCount[];
  privilege: DeliveryFleetCount[];
  assignmentPhases: DeliveryFleetCount[];
}

export interface DeliveryFleet {
  summary: DeliveryFleetSummary;
  clusters: DeliveryFleetCluster[];
  attention: DeliveryFleetAttention[];
  distributions: DeliveryFleetDistributions;
}

export interface PageParams {
  limit?: number;
  offset?: number;
}

function idempotencyHeaders(key?: string): Record<string, string> {
  return key ? { "Idempotency-Key": key } : {};
}

function mutationHeaders(
  etag: string | number,
  key?: string,
): Record<string, string> {
  const quoted =
    typeof etag === "number"
      ? `"${etag}"`
      : etag.startsWith('"')
        ? etag
        : `"${etag}"`;
  return { "If-Match": quoted, ...idempotencyHeaders(key) };
}

function entity<T>(data: DataEnvelope<T>, headers: unknown): EntityResponse<T> {
  const candidate = headers as
    { etag?: unknown; get?: (name: string) => unknown } | undefined;
  const value = candidate?.etag ?? candidate?.get?.("etag");
  return {
    data: data.data,
    etag: typeof value === "string" ? value : undefined,
  };
}

export async function listDeliverySources(
  projectId: string,
  params: PageParams & { status?: string } = {},
) {
  const response = await api.get<DeliveryPage<DeliverySource>>(
    "/delivery/sources/",
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function createDeliverySource(
  body: CreateDeliverySourceRequest,
  key?: string,
) {
  const response = await api.post<DataEnvelope<DeliverySource>>(
    "/delivery/sources/",
    body,
    {
      headers: idempotencyHeaders(key),
    },
  );
  return response.data.data;
}

export async function getDeliverySource(projectId: string, id: string) {
  const response = await api.get<DataEnvelope<DeliverySource>>(
    `/delivery/sources/${id}/`,
    {
      params: { project_id: projectId },
    },
  );
  return response.data.data;
}

export async function deleteDeliverySource(
  projectId: string,
  id: string,
  key?: string,
) {
  await api.delete(`/delivery/sources/${id}/`, {
    params: { project_id: projectId },
    headers: idempotencyHeaders(key),
  });
}

export async function rotateDeliverySourceCredential(
  id: string,
  body: RotateSourceCredentialRequest,
  key?: string,
) {
  const response = await api.post<DataEnvelope<DeliverySource>>(
    `/delivery/sources/${id}/rotate-credential/`,
    body,
    {
      headers: idempotencyHeaders(key),
    },
  );
  return response.data.data;
}

export async function verifyDeliverySource(
  id: string,
  body: { project_id: string; requested_revision: string; chart?: string },
  key?: string,
) {
  const response = await api.post<DataEnvelope<SourceVerification>>(
    `/delivery/sources/${id}/verify/`,
    body,
    {
      headers: idempotencyHeaders(key),
    },
  );
  return response.data.data;
}

export async function listComponentBundles(
  projectId: string,
  params: PageParams = {},
) {
  const response = await api.get<DeliveryPage<ComponentBundle>>(
    "/delivery/bundles/",
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function createComponentBundle(
  projectId: string,
  body: { name: string; description?: string },
  key?: string,
) {
  const response = await api.post<DataEnvelope<ComponentBundle>>(
    "/delivery/bundles/",
    {
      project_id: projectId,
      ...body,
    },
    { headers: idempotencyHeaders(key) },
  );
  return response.data.data;
}

export async function getComponentBundle(projectId: string, id: string) {
  const response = await api.get<DataEnvelope<ComponentBundle>>(
    `/delivery/bundles/${id}/`,
    {
      params: { project_id: projectId },
    },
  );
  return response.data.data;
}

export async function listComponentBundleVersions(
  projectId: string,
  bundleId: string,
  params: PageParams = {},
) {
  const response = await api.get<DeliveryPage<ComponentBundleVersion>>(
    `/delivery/bundles/${bundleId}/versions/`,
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function getComponentBundleVersion(
  projectId: string,
  bundleId: string,
  versionId: string,
) {
  const response = await api.get<DataEnvelope<ComponentBundleVersion>>(
    `/delivery/bundles/${bundleId}/versions/${versionId}/`,
    {
      params: { project_id: projectId },
    },
  );
  return response.data.data;
}

export async function createComponentBundleVersion(
  bundleId: string,
  body: CreateBundleVersionRequest,
  key?: string,
) {
  const response = await api.post<DataEnvelope<ComponentBundleVersion>>(
    `/delivery/bundles/${bundleId}/versions/`,
    body,
    {
      headers: idempotencyHeaders(key),
    },
  );
  return response.data.data;
}

export async function listDeliveryTargets(
  projectId: string,
  params: PageParams = {},
) {
  const response = await api.get<DeliveryPage<DeliveryTarget>>(
    "/delivery/targets/",
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function createDeliveryTarget(
  body: DeliveryTargetRequest & DeliveryContracts["DeliveryTargetWrite"],
  key?: string,
) {
  const response = await api.post<DataEnvelope<DeliveryTarget>>(
    "/delivery/targets/",
    body,
    {
      headers: idempotencyHeaders(key),
    },
  );
  return entity(response.data, response.headers);
}

export async function getDeliveryTarget(projectId: string, id: string) {
  const response = await api.get<DataEnvelope<DeliveryTarget>>(
    `/delivery/targets/${id}/`,
    {
      params: { project_id: projectId },
    },
  );
  return entity(response.data, response.headers);
}

export async function updateDeliveryTarget(
  id: string,
  body: Partial<DeliveryTargetRequest>,
  etag: string | number,
  key?: string,
) {
  const response = await api.patch<DataEnvelope<DeliveryTarget>>(
    `/delivery/targets/${id}/`,
    body,
    {
      headers: mutationHeaders(etag, key),
    },
  );
  return entity(response.data, response.headers);
}

export async function deleteDeliveryTarget(
  projectId: string,
  id: string,
  etag: string | number,
  key?: string,
) {
  const response = await api.delete<
    DataEnvelope<{
      id: string;
      deletionState: string;
      resourceVersion: number;
      deploymentCount: number;
    }>
  >(`/delivery/targets/${id}/`, {
    params: { project_id: projectId },
    headers: mutationHeaders(etag, key),
  });
  return response.data.data;
}

export async function orphanDeliveryTarget(
  projectId: string,
  id: string,
  etag: string | number,
  key?: string,
) {
  const response = await api.post<
    DataEnvelope<{ id: string; deletionState: string; resourceVersion: number }>
  >(
    `/delivery/targets/${id}/orphan/`,
    { project_id: projectId },
    { headers: mutationHeaders(etag, key) },
  );
  return response.data.data;
}

export async function previewDeliveryTarget(
  projectId: string,
  id: string,
  params: PlacementPreviewPageParams = {},
) {
  const response = await api.post<DataEnvelope<PlacementPreview>>(
    `/delivery/targets/${id}/preview/`,
    undefined,
    {
      params: {
        project_id: projectId,
        ...(params.pageSize ? { page_size: params.pageSize } : {}),
        ...(params.cursor ? { cursor: params.cursor } : {}),
      },
    },
  );
  return response.data.data;
}

export async function startDeliveryRollout(
  targetId: string,
  body: {
    project_id: string;
    preview_digest: string;
    confirm_all_clusters: boolean;
    strategy: RolloutStrategyRequest;
  } & DeliveryContracts["DeliveryRolloutStart"],
  targetGeneration: number,
  key: string,
) {
  const response = await api.post<DataEnvelope<DeliveryFrozenRollout>>(
    `/delivery/targets/${targetId}/rollouts/`,
    body,
    {
      headers: mutationHeaders(targetGeneration, key),
    },
  );
  return response.data.data;
}

export async function listDeliveryRollouts(
  projectId: string,
  params: PageParams & { state?: RolloutState } = {},
) {
  const response = await api.get<DeliveryPage<DeliveryRollout>>(
    "/delivery/rollouts/",
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function getDeliveryRollout(projectId: string, id: string) {
  const response = await api.get<DataEnvelope<DeliveryRolloutDetail>>(
    `/delivery/rollouts/${id}/`,
    {
      params: { project_id: projectId },
    },
  );
  return entity(response.data, response.headers);
}

export async function listDeliveryRolloutClusters(
  projectId: string,
  id: string,
  params: PageParams = {},
) {
  const response = await api.get<DeliveryPage<DeliveryRolloutCluster>>(
    `/delivery/rollouts/${id}/clusters/`,
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function listDeliveryRolloutEvents(
  projectId: string,
  id: string,
  params: PageParams = {},
) {
  const response = await api.get<DeliveryPage<DeliveryRolloutEvent>>(
    `/delivery/rollouts/${id}/events/`,
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function actOnDeliveryRollout(
  projectId: string,
  id: string,
  action: "pause" | "resume" | "abort" | "retry" | "rollback",
  etag: string | number,
  reasonCode: string,
  key?: string,
) {
  const response = await api.post<
    DataEnvelope<{ rollout: DeliveryRollout; event: DeliveryRolloutEvent }>
  >(
    `/delivery/rollouts/${id}/${action}/`,
    { project_id: projectId, reason_code: reasonCode },
    { headers: mutationHeaders(etag, key) },
  );
  return entity(response.data, response.headers);
}

export async function approveDeliveryRollout(
  id: string,
  body: {
    project_id: string;
    cohort: number;
    binding_digest: string;
    decision: "approved" | "rejected";
    expires_at: string;
  } & DeliveryContracts["DeliveryRolloutApproval"],
  etag: string | number,
  key?: string,
) {
  const response = await api.post<
    DataEnvelope<{
      rollout: DeliveryRollout;
      approval: DeliveryRolloutApproval;
      event: DeliveryRolloutEvent;
    }>
  >(`/delivery/rollouts/${id}/approve/`, body, {
    headers: mutationHeaders(etag, key),
  });
  return entity(response.data, response.headers);
}

export async function listClusterDeployments(
  projectId: string,
  params: PageParams & { cluster_id?: string; phase?: DeploymentPhase } = {},
) {
  const response = await api.get<DeliveryPage<ClusterDeployment>>(
    "/delivery/deployments/",
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function getClusterDeployment(projectId: string, id: string) {
  const response = await api.get<DataEnvelope<ClusterDeploymentDetail>>(
    `/delivery/deployments/${id}/`,
    {
      params: { project_id: projectId },
    },
  );
  return entity(response.data, response.headers);
}

export async function listClusterDeploymentEvents(
  projectId: string,
  id: string,
  params: PageParams = {},
) {
  const response = await api.get<DeliveryPage<ClusterDeploymentEvent>>(
    `/delivery/deployments/${id}/events/`,
    {
      params: { project_id: projectId, ...params },
    },
  );
  return response.data;
}

export async function actOnClusterDeployment(
  projectId: string,
  id: string,
  action: "reconcile" | "suspend",
  etag: string | number,
  reasonCode: string,
  key?: string,
) {
  const response = await api.post<
    DataEnvelope<{
      deployment: ClusterDeployment;
      event: ClusterDeploymentEvent;
    }>
  >(
    `/delivery/deployments/${id}/${action}/`,
    { project_id: projectId, reason_code: reasonCode },
    { headers: mutationHeaders(etag, key) },
  );
  return entity(response.data, response.headers);
}

export async function getClusterDeliveryInventory(
  projectId: string,
  clusterId: string,
) {
  const response = await api.get<DataEnvelope<ClusterDeliveryInventory>>(
    `/delivery/clusters/${clusterId}/inventory/`,
    {
      params: { project_id: projectId },
    },
  );
  return response.data.data;
}

export async function getDeliverySystemCompatibility() {
  const response = await api.get<DataEnvelope<DeliverySystemCompatibility>>(
    "/delivery/system/compatibility/",
  );
  return response.data.data;
}

export async function getDeliveryFleet() {
  const response = await api.get<DataEnvelope<DeliveryFleet>>(
    "/delivery/fleet/",
  );
  return response.data.data;
}

export function rolloutIsTerminal(state: RolloutState): boolean {
  return [
    "rejected",
    "aborted",
    "succeeded",
    "failed",
    "rolled_back",
    "rollback_failed",
  ].includes(state);
}

// Keep the post-camelization view models tied to the generated wire key sets.
// These fail compilation if UI code invents a response field the OpenAPI
// contract does not publish.
const deliveryTargetMatchesWire: AssertNoPhantomWireKeys<
  DeliveryTarget,
  DeliveryContracts["DeliveryTarget"]
> = true;
const placementDecisionMatchesWire: AssertNoPhantomWireKeys<
  PlacementDecision,
  DeliveryContracts["DeliveryPreviewDecision"]
> = true;
const placementPreviewMatchesWire: AssertNoPhantomWireKeys<
  PlacementPreview,
  DeliveryContracts["DeliveryTargetPreview"]
> = true;
const frozenRolloutMatchesWire: AssertNoPhantomWireKeys<
  DeliveryFrozenRollout,
  DeliveryContracts["DeliveryFrozenRollout"]
> = true;
const deliveryRolloutMatchesWire: AssertNoPhantomWireKeys<
  DeliveryRollout,
  DeliveryContracts["DeliveryRollout"]
> = true;
const deliveryRolloutEventMatchesWire: AssertNoPhantomWireKeys<
  DeliveryRolloutEvent,
  DeliveryContracts["DeliveryRolloutEvent"]
> = true;
const rolloutClusterMatchesWire: AssertNoPhantomWireKeys<
  DeliveryRolloutCluster,
  DeliveryContracts["DeliveryRolloutCluster"]
> = true;
const clusterDeploymentMatchesWire: AssertNoPhantomWireKeys<
  ClusterDeployment,
  DeliveryContracts["ClusterDeployment"]
> = true;
const deploymentEventMatchesWire: AssertNoPhantomWireKeys<
  ClusterDeploymentEvent,
  DeliveryContracts["ClusterDeploymentEvent"]
> = true;
const controllerInventoryMatchesWire: AssertNoPhantomWireKeys<
  DeliveryControllerInventory,
  DeliveryContracts["DeliveryControllerInventory"]
> = true;
const clusterInventoryMatchesWire: AssertNoPhantomWireKeys<
  ClusterDeliveryInventory,
  DeliveryContracts["DeliveryClusterInventory"]
> = true;
const systemCompatibilityMatchesWire: AssertNoPhantomWireKeys<
  DeliverySystemCompatibility,
  DeliveryContracts["DeliverySystemCompatibility"]
> = true;
const deliveryFleetMatchesWire: AssertNoPhantomWireKeys<
  DeliveryFleet,
  DeliveryContracts["DeliveryFleet"]
> = true;

void [
  deliveryTargetMatchesWire,
  placementDecisionMatchesWire,
  placementPreviewMatchesWire,
  frozenRolloutMatchesWire,
  deliveryRolloutMatchesWire,
  deliveryRolloutEventMatchesWire,
  rolloutClusterMatchesWire,
  clusterDeploymentMatchesWire,
  deploymentEventMatchesWire,
  controllerInventoryMatchesWire,
  clusterInventoryMatchesWire,
  systemCompatibilityMatchesWire,
  deliveryFleetMatchesWire,
];
