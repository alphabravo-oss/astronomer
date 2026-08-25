import { camelizeKeys } from "@/lib/camelize";
import * as generated from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type {
  AssertNoPhantomWireKeys,
  CamelizeKeys,
} from "@/types/wire-contract";

type DeliveryContracts = OpenAPIComponents["schemas"];

/**
 * Flux-native delivery wire contract.
 *
 * Request fields deliberately retain their documented snake_case spelling.
 * Generated responses retain exact wire keys. The mapping functions below
 * deliberately build camelCase view models. Secret material exists
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

export type DeliverySource = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliverySource"]
>;

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

export type DeliveryTarget = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryTarget"]>,
  | "placement"
  | "rolloutPolicy"
  | "reconciliationPolicy"
  | "maintenanceWindowPolicy"
> & {
  placement: Placement;
  rolloutPolicy: { approvalRequired: boolean };
  reconciliationPolicy: ReconciliationPolicy;
  maintenanceWindowPolicy: Record<string, unknown>;
};

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

export type DeliveryRollout = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryRollout"]>,
  "strategy" | "state"
> & {
  strategy: RolloutStrategy;
  state: RolloutState;
};

export type DeliveryRolloutApproval = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryRolloutApprovalRecord"]
>;

export type DeliveryRolloutEvent = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryRolloutEvent"]
>;

export interface DeliveryFrozenPlan {
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
}

export type DeliveryRolloutDetail = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryRolloutDetail"]>,
  "rollout" | "frozenPlan" | "approvals" | "timeline"
> & {
  rollout: DeliveryRollout;
  frozenPlan: DeliveryFrozenPlan;
  approvals: DeliveryRolloutApproval[];
  timeline: DeliveryRolloutEvent[];
};

export type DeliveryFrozenRollout = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryFrozenRollout"]>,
  "desired" | "strategy" | "approval" | "cohorts" | "clusters"
> & {
  desired: DeliveryFrozenPlan["desired"];
  strategy: RolloutStrategy;
  approval: DeliveryFrozenPlan["approval"];
  cohorts: DeliveryFrozenPlan["cohorts"];
  clusters: Array<{
    clusterId: string;
    cohort: number;
    order: number;
    previous?: Record<string, unknown>;
  }>;
};

export type DeliveryRolloutCluster = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryRolloutCluster"]>,
  "state"
> & { state: RolloutClusterState };

export type DeliveryConditionView = CamelizeKeys<
  DeliveryContracts["DeliveryCondition"]
>;

export type ClusterDeployment = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterDeployment"]>,
  "conditions"
> & { conditions: DeliveryConditionView[] };

export type ClusterDeploymentEvent = CamelizeKeys<
  OpenAPIComponents["schemas"]["ClusterDeploymentEvent"]
>;

export type ClusterDeploymentDetail = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterDeploymentDetail"]>,
  "deployment" | "events"
> & {
  deployment: ClusterDeployment;
  events: ClusterDeploymentEvent[];
};

export type DeliveryControllerInventory = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryControllerInventory"]
>;

export type ClusterDeliveryInventory = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryClusterInventory"]>,
  "deployments"
> & { deployments: ClusterDeployment[] };

export type DeliverySystemCompatibility = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliverySystemCompatibility"]
>;

export type DeliveryEstateCount = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateCount"]
>;

export type DeliveryEstateSummary = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateSummary"]
>;

export type DeliveryEstateCluster = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateCluster"]
>;

export type DeliveryEstateAttention = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateAttention"]
>;

export type DeliveryEstateDistributions = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateDistributions"]
>;

export type DeliveryEstate = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstate"]
>;

export interface PageParams {
  limit?: number;
  offset?: number;
}

function quotedETag(etag: string | number): string {
  if (typeof etag === "number") return `"${etag}"`;
  return etag.startsWith('"') ? etag : `"${etag}"`;
}

function optionalIdempotencyHeader(key?: string): { "Idempotency-Key": string } {
  return { "Idempotency-Key": key ?? createIdempotencyKey() };
}

function requiredEnvelopeData<T>(envelope: { data?: T }): T {
  if (envelope.data === undefined) {
    throw new Error("Delivery API returned an empty data envelope.");
  }
  return envelope.data;
}

function mapPage<TWire, TView>(
  page: {
    data: TWire[];
    count: number;
    next: string | null;
    previous: string | null;
    total_known: boolean;
  },
  mapper: (wire: TWire) => TView,
): DeliveryPage<TView> {
  return {
    data: page.data.map(mapper),
    count: page.count,
    next: page.next,
    previous: page.previous,
    totalKnown: page.total_known,
  };
}

// Each operation crosses the raw generated-client boundary through a
// domain-named mapper. This keeps wire casing out of components and makes the
// transport migration independently replaceable when the view models evolve.
function mapSource(wire: DeliveryContracts["DeliverySource"]): DeliverySource {
  return camelizeKeys(wire) as unknown as DeliverySource;
}
function mapBundle(wire: DeliveryContracts["DeliveryBundle"]): ComponentBundle {
  return camelizeKeys(wire) as unknown as ComponentBundle;
}
function mapBundleVersion(
  wire: DeliveryContracts["DeliveryBundleVersion"],
): ComponentBundleVersion {
  return camelizeKeys(wire) as unknown as ComponentBundleVersion;
}
function mapTarget(wire: DeliveryContracts["DeliveryTarget"]): DeliveryTarget {
  return camelizeKeys(wire) as unknown as DeliveryTarget;
}
function mapPreview(
  wire: DeliveryContracts["DeliveryTargetPreview"],
): PlacementPreview {
  return camelizeKeys(wire) as unknown as PlacementPreview;
}
function mapRollout(
  wire: DeliveryContracts["DeliveryRollout"],
): DeliveryRollout {
  return camelizeKeys(wire) as unknown as DeliveryRollout;
}
function mapRolloutEvent(
  wire: DeliveryContracts["DeliveryRolloutEvent"],
): DeliveryRolloutEvent {
  return camelizeKeys(wire) as unknown as DeliveryRolloutEvent;
}
function mapRolloutCluster(
  wire: DeliveryContracts["DeliveryRolloutCluster"],
): DeliveryRolloutCluster {
  return camelizeKeys(wire) as unknown as DeliveryRolloutCluster;
}
function mapFrozenRollout(
  wire: DeliveryContracts["DeliveryFrozenRollout"],
): DeliveryFrozenRollout {
  const mapped = camelizeKeys(wire) as unknown as CamelizeKeys<typeof wire>;
  const { idempotencyKey: _idempotencyKey, ...safe } = mapped;
  const { trustPolicy, ...source } = mapped.desired.source;
  return {
    ...safe,
    desired: {
      ...mapped.desired,
      source: { ...source, trust: trustPolicy },
    },
  } as DeliveryFrozenRollout;
}
function mapRolloutDetail(
  wire: DeliveryContracts["DeliveryRolloutDetail"],
): DeliveryRolloutDetail {
  const mapped = camelizeKeys(wire) as unknown as CamelizeKeys<typeof wire>;
  return {
    ...mapped,
    rollout: mapRollout(wire.rollout),
    frozenPlan: mapFrozenRollout(wire.frozen_plan),
    approvals: wire.approvals.map((item) =>
      camelizeKeys(item),
    ) as unknown as DeliveryRolloutApproval[],
    timeline: wire.timeline.map(mapRolloutEvent),
  } as DeliveryRolloutDetail;
}
function mapDeployment(
  wire: DeliveryContracts["ClusterDeployment"],
): ClusterDeployment {
  return camelizeKeys(wire) as unknown as ClusterDeployment;
}
function mapDeploymentEvent(
  wire: DeliveryContracts["ClusterDeploymentEvent"],
): ClusterDeploymentEvent {
  return camelizeKeys(wire) as unknown as ClusterDeploymentEvent;
}
function mapDeploymentDetail(
  wire: DeliveryContracts["ClusterDeploymentDetail"],
): ClusterDeploymentDetail {
  return {
    ...camelizeKeys(wire),
    deployment: mapDeployment(wire.deployment),
    events: wire.events.map(mapDeploymentEvent),
  } as ClusterDeploymentDetail;
}
function mapInventory(
  wire: DeliveryContracts["DeliveryClusterInventory"],
): ClusterDeliveryInventory {
  return {
    ...camelizeKeys(wire),
    deployments: wire.deployments.map(mapDeployment),
  } as unknown as ClusterDeliveryInventory;
}
function mapCompatibility(
  wire: DeliveryContracts["DeliverySystemCompatibility"],
): DeliverySystemCompatibility {
  return camelizeKeys(wire) as unknown as DeliverySystemCompatibility;
}
function mapEstate(wire: DeliveryContracts["DeliveryEstate"]): DeliveryEstate {
  return camelizeKeys(wire) as unknown as DeliveryEstate;
}

function entityFromResponse<TWire, TView>(
  response: generated.OpenAPIResponse<{ data?: TWire }>,
  mapper: (wire: TWire) => TView,
): EntityResponse<TView> {
  const headers = response.headers as {
    etag?: unknown;
    get?: (name: string) => unknown;
  };
  const value = headers.etag ?? headers.get?.("etag");
  return {
    data: mapper(requiredEnvelopeData(response.data)),
    etag: typeof value === "string" ? value : undefined,
  };
}

export async function listDeliverySources(
  projectId: string,
  params: PageParams & { status?: string } = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliverySources({
    query: {
      project_id: projectId,
      limit: params.limit,
      offset: params.offset,
      status: params.status as
        "pending" | "ready" | "degraded" | "revoked" | undefined,
    },
    signal,
  });
  return mapPage(wire, mapSource);
}

export async function createDeliverySource(
  body: CreateDeliverySourceRequest,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliverySources({
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapSource(requiredEnvelopeData(wire));
}

export async function getDeliverySource(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliverySourcesById({
    path: { id },
    query: { project_id: projectId },
    signal,
  });
  return mapSource(requiredEnvelopeData(wire));
}

export async function deleteDeliverySource(
  projectId: string,
  id: string,
  key?: string,
  signal?: AbortSignal,
) {
  await generated.deleteDeliverySourcesById({
    path: { id },
    query: { project_id: projectId },
    headers: optionalIdempotencyHeader(key),
    signal,
  });
}

export async function rotateDeliverySourceCredential(
  id: string,
  body: RotateSourceCredentialRequest,
  key?: string,
  signal?: AbortSignal,
) {
  if (!["basic", "bearer", "ssh"].includes(body.auth_mode)) {
    throw new Error("Credential rotation requires basic, bearer, or SSH auth.");
  }
  const wire = await generated.postDeliverySourcesByIdRotateCredential({
    path: { id },
    body: body as DeliveryContracts["DeliverySourceCredentialRotate"],
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapSource(requiredEnvelopeData(wire));
}

export async function verifyDeliverySource(
  id: string,
  body: { project_id: string; requested_revision: string; chart?: string },
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliverySourcesByIdVerify({
    path: { id },
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return camelizeKeys(
    requiredEnvelopeData(wire),
  ) as unknown as SourceVerification;
}

export async function listComponentBundles(
  projectId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundles({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapBundle);
}

export async function createComponentBundle(
  projectId: string,
  body: { name: string; description?: string },
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryBundles({
    body: { project_id: projectId, ...body },
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapBundle(requiredEnvelopeData(wire));
}

export async function getComponentBundle(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundlesById({
    path: { id },
    query: { project_id: projectId },
    signal,
  });
  return mapBundle(requiredEnvelopeData(wire));
}

export async function listComponentBundleVersions(
  projectId: string,
  bundleId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundlesByIdVersions({
    path: { id: bundleId },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapBundleVersion);
}

export async function getComponentBundleVersion(
  projectId: string,
  bundleId: string,
  versionId: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundlesByIdVersionsByVersionId({
    path: { id: bundleId, versionId },
    query: { project_id: projectId },
    signal,
  });
  return mapBundleVersion(requiredEnvelopeData(wire));
}

export async function createComponentBundleVersion(
  bundleId: string,
  body: CreateBundleVersionRequest,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryBundlesByIdVersions({
    path: { id: bundleId },
    query: { project_id: body.project_id },
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapBundleVersion(requiredEnvelopeData(wire));
}

export async function listDeliveryTargets(
  projectId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryTargets({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapTarget);
}

export async function createDeliveryTarget(
  body: DeliveryTargetRequest & DeliveryContracts["DeliveryTargetWrite"],
  key?: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "postDeliveryTargets",
    {
      body,
      headerParams: optionalIdempotencyHeader(key),
      signal,
    },
  );
  return entityFromResponse(response, mapTarget);
}

export async function getDeliveryTarget(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "getDeliveryTargetsById",
    {
      path: { id },
      query: { project_id: projectId },
      signal,
    },
  );
  return entityFromResponse(response, mapTarget);
}

export async function updateDeliveryTarget(
  id: string,
  body: Partial<DeliveryTargetRequest>,
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  if (!body.project_id) {
    throw new Error("A project_id is required to update a delivery target.");
  }
  const response = await generated.executeOpenAPIOperationWithResponse(
    "patchDeliveryTargetsById",
    {
      path: { id },
      query: { project_id: body.project_id },
      body,
      headerParams: {
        "If-Match": quotedETag(etag),
        ...optionalIdempotencyHeader(key),
      },
      signal,
    },
  );
  return entityFromResponse(response, mapTarget);
}

export async function deleteDeliveryTarget(
  projectId: string,
  id: string,
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.deleteDeliveryTargetsById({
    path: { id },
    query: { project_id: projectId },
    headerParams: {
      "If-Match": quotedETag(etag),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
  return camelizeKeys(requiredEnvelopeData(wire)) as unknown as {
    id: string;
    deletionState: string;
    resourceVersion: number;
    deploymentCount: number;
  };
}

export async function orphanDeliveryTarget(
  projectId: string,
  id: string,
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryTargetsByIdOrphan({
    path: { id },
    query: { project_id: projectId },
    headerParams: {
      "If-Match": quotedETag(etag),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
  return camelizeKeys(requiredEnvelopeData(wire)) as unknown as {
    id: string;
    deletionState: string;
    resourceVersion: number;
  };
}

export async function previewDeliveryTarget(
  projectId: string,
  id: string,
  params: PlacementPreviewPageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryTargetsByIdPreview({
    path: { id },
    query: {
      project_id: projectId,
      ...(params.pageSize ? { page_size: params.pageSize } : {}),
      ...(params.cursor ? { cursor: params.cursor } : {}),
    },
    signal,
  });
  return mapPreview(requiredEnvelopeData(wire));
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
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryTargetsByIdRollouts({
    path: { id: targetId },
    body,
    headerParams: {
      "If-Match": quotedETag(targetGeneration),
      "Idempotency-Key": key,
    },
    signal,
  });
  return mapFrozenRollout(requiredEnvelopeData(wire));
}

export async function listDeliveryRollouts(
  projectId: string,
  params: PageParams & { state?: RolloutState } = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryRollouts({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapRollout);
}

export async function getDeliveryRollout(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "getDeliveryRolloutsById",
    {
      path: { id },
      query: { project_id: projectId },
      signal,
    },
  );
  return entityFromResponse(response, mapRolloutDetail);
}

export async function listDeliveryRolloutClusters(
  projectId: string,
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryRolloutsByIdClusters({
    path: { id },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapRolloutCluster);
}

export async function listDeliveryRolloutEvents(
  projectId: string,
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryRolloutsByIdEvents({
    path: { id },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapRolloutEvent);
}

export async function actOnDeliveryRollout(
  projectId: string,
  id: string,
  action: "pause" | "resume" | "abort" | "retry" | "rollback",
  etag: string | number,
  reasonCode: string,
  key?: string,
  signal?: AbortSignal,
) {
  const common = {
    path: { id },
    body: { project_id: projectId, reason_code: reasonCode },
    signal,
  };
  const ifMatch = quotedETag(etag);
  const response =
    action === "pause"
      ? await generated.executeOpenAPIOperationWithResponse(
          "postDeliveryRolloutsByIdPause",
          {
            ...common,
            headerParams: {
              "If-Match": ifMatch,
              ...optionalIdempotencyHeader(key),
            },
          },
        )
      : action === "resume"
        ? await generated.executeOpenAPIOperationWithResponse(
            "postDeliveryRolloutsByIdResume",
            {
              ...common,
              headerParams: {
                "If-Match": ifMatch,
                ...optionalIdempotencyHeader(key),
              },
            },
          )
        : action === "abort"
          ? await generated.executeOpenAPIOperationWithResponse(
              "postDeliveryRolloutsByIdAbort",
              {
                ...common,
                headerParams: {
                  "If-Match": ifMatch,
                  ...optionalIdempotencyHeader(key),
                },
              },
            )
          : action === "retry"
            ? await generated.executeOpenAPIOperationWithResponse(
                "postDeliveryRolloutsByIdRetry",
                {
                  ...common,
                  headerParams: {
                    "If-Match": ifMatch,
                    ...optionalIdempotencyHeader(key),
                  },
                },
              )
            : await generated.executeOpenAPIOperationWithResponse(
                "postDeliveryRolloutsByIdRollback",
                {
                  ...common,
                  headerParams: {
                    "If-Match": ifMatch,
                    ...optionalIdempotencyHeader(key),
                  },
                },
              );
  return camelizeKeys(requiredEnvelopeData(response.data));
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
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "postDeliveryRolloutsByIdApprove",
    {
      path: { id },
      body,
      headerParams: {
        "If-Match": quotedETag(etag),
        ...optionalIdempotencyHeader(key),
      },
      signal,
    },
  );
  return camelizeKeys(requiredEnvelopeData(response.data));
}

export async function listClusterDeployments(
  projectId: string,
  params: PageParams & { cluster_id?: string; phase?: DeploymentPhase } = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryDeployments({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapDeployment);
}

export async function getClusterDeployment(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "getDeliveryDeploymentsById",
    {
      path: { id },
      query: { project_id: projectId },
      signal,
    },
  );
  return entityFromResponse(response, mapDeploymentDetail);
}

export async function listClusterDeploymentEvents(
  projectId: string,
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryDeploymentsByIdEvents({
    path: { id },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapDeploymentEvent);
}

export async function actOnClusterDeployment(
  projectId: string,
  id: string,
  action: "reconcile" | "suspend",
  etag: string | number,
  reasonCode: string,
  key?: string,
  signal?: AbortSignal,
) {
  const args = {
    path: { id },
    body: { project_id: projectId, reason_code: reasonCode },
    headerParams: {
      "If-Match": quotedETag(etag),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  };
  const response =
    action === "reconcile"
      ? await generated.executeOpenAPIOperationWithResponse(
          "postDeliveryDeploymentsByIdReconcile",
          args,
        )
      : await generated.executeOpenAPIOperationWithResponse(
          "postDeliveryDeploymentsByIdSuspend",
          args,
        );
  return camelizeKeys(requiredEnvelopeData(response.data));
}

export async function getClusterDeliveryInventory(
  projectId: string,
  clusterId: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryClustersByClusterIdInventory({
    path: { clusterId },
    query: { project_id: projectId },
    signal,
  });
  return mapInventory(requiredEnvelopeData(wire));
}

export async function getDeliverySystemCompatibility(signal?: AbortSignal) {
  const wire = await generated.getDeliverySystemCompatibility({ signal });
  return mapCompatibility(requiredEnvelopeData(wire));
}

export async function getDeliveryEstate(signal?: AbortSignal) {
  const wire = await generated.getDeliveryEstate({ signal });
  return mapEstate(requiredEnvelopeData(wire));
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
  DeliveryEstate,
  DeliveryContracts["DeliveryEstate"]
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
