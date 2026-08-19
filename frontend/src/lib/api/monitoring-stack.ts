/**
 * Monitoring-stack lifecycle client — the 18 install/upgrade/replace/
 * uninstall/preview/status endpoints across the three stack families, plus
 * the three operations-queue endpoints that make them observable.
 *
 * Backend:
 *   internal/handler/monitoring_stack_cluster.go  (per-cluster kube-prometheus-stack)
 *   internal/handler/monitoring_stack_shared.go   (shared Thanos + Alertmanager + Grafana,
 *                                                  all driven by sharedStackLifecycle)
 *   internal/handler/monitoring_operations.go     (the async queue behind all of them)
 *
 * THREE THINGS ABOUT THESE ENDPOINTS ARE NOT THE HOUSE DEFAULT — read before editing.
 *
 * 1. REQUEST BODIES ARE camelCase, not snake_case. The repo convention is that
 *    request bodies are spelled snake_case by hand (there is no request
 *    interceptor; only responses are camelized). These handlers are the
 *    exception: MonitoringStackRequest / SharedThanosStackRequest /
 *    SharedAlertmanagerRequest in internal/handler/monitoring.go carry
 *    camelCase json tags (`managementClusterId`, `storageConfigId`,
 *    `thanosSidecarEnabled`, ...). Sending snake_case here silently decodes to
 *    the zero value and the handler quietly applies its defaults instead — e.g.
 *    a snake_case `storage_config_id` reads as absent and the shared-Thanos
 *    payload builder rejects the request as "storageConfigId is required".
 *    The query parameters on ListOperations are camelCase for the same reason
 *    (`targetType`/`targetKey`, read verbatim off r.URL.Query()).
 *
 * 2. NOTHING HERE IS IN docs/openapi.yaml. None of the 21 paths are documented,
 *    so there are no generated schemas to import and the view types below are
 *    hand-written by necessity — they shadow nothing, and wire-contract.ts has
 *    nothing to bind them to. If these paths are ever added to the spec, bind
 *    the response types in src/types/wire-contract.ts at that point.
 *
 * 3. THE MUTATIONS ARE ASYNCHRONOUS. install/upgrade/replace/uninstall return
 *    202 with a MonitoringOperation row in `pending`; the actual Helm work runs
 *    in the server-side reconciler (30s tick, kicked immediately on enqueue)
 *    and takes tens of seconds to minutes. The returned row is a receipt, not a
 *    result. Track it with `useMonitoringOperationTracker`
 *    (src/components/monitoring/hooks.ts) — never treat a resolved promise from
 *    installClusterStack() as "the stack is installed".
 *
 * Response envelopes: every endpoint here goes through RespondJSON, i.e.
 * `{ data: ... }`. ListOperations goes through RespondList, i.e.
 * `{ data: [...], pagination: {...} }`.
 *
 * 4. THE PREVIEW RESPONSES MUST NOT BE CAMELIZED, and are carved out of the
 *    global response interceptor by `isMonitoringPreviewPath` in src/lib/api.ts.
 *    The handlers' own envelope keys are camelCase already, but `values` is a
 *    rendered HELM VALUES MAP — upstream chart configuration, where the keys are
 *    data. Shared Alertmanager's `values.config` is snake_case by construction
 *    (internal/handler/monitoring_stack_shared.go:822-868 renders
 *    `resolve_timeout`, `group_by`, `group_wait`, `group_interval`,
 *    `repeat_interval`, `webhook_configs`, `email_configs`, `send_resolved`) and
 *    the per-cluster preview keys prometheusSpec.externalLabels by
 *    req.ClusterLabel, which defaults to the literal `cluster_id`
 *    (internal/handler/monitoring_stack_cluster.go:416-418, :450). Camelizing
 *    those turns the "Rendered Helm values" pane into YAML the server will never
 *    apply, while the `spec <hash>` beside it is computed server-side over the
 *    un-mangled map. Same reasoning as the pre-existing `/k8s/` carve-out.
 */
import api from '@/lib/api';
import { unwrapData } from '@/lib/api/errors';
import type { APIResponse } from '@/types';

// ─────────────────────────────────────────────────────────────────────
// Operation queue
// ─────────────────────────────────────────────────────────────────────

/**
 * The complete status alphabet of monitoring_operations.status. Source of
 * truth: internal/operationstate/state.go, surfaced through
 * internal/handler/operation_status.go. There are FIVE, not two:
 *
 *   pending    — enqueued, not yet claimed by the reconciler.
 *   running    — claimed; Helm apply + readiness + smoke-check in progress.
 *   completed  — terminal success.
 *   failed     — terminal failure; `errorMessage` carries the real error.
 *                NOTE: the reconciler may immediately requeue a failed row
 *                back to `pending` when attemptCount < the backend's
 *                maxRetryAttempts policy, so a `failed` observation is not
 *                automatically the end. See isSettledFailure().
 *   superseded — a newer operation for the same target took over before this
 *                one ran. Terminal, retryable, and NOT the user's fault:
 *                errorMessage is the fixed string
 *                "superseded by newer operation for target".
 */
export type MonitoringOperationStatus =
  | 'pending'
  | 'running'
  | 'completed'
  | 'failed'
  | 'superseded';

/** monitoring_operations.target_type. One per stack family. */
export type MonitoringOperationTargetType =
  | 'cluster_stack'
  | 'shared_thanos'
  | 'shared_alertmanager'
  | 'shared_grafana';

/** monitoring_operations.operation_type — the four mutating lifecycle verbs. */
export type MonitoringOperationType = 'install' | 'upgrade' | 'replace' | 'uninstall';

/**
 * One row of monitoring_operation_events — the reconciler's stage log
 * (queue / render / install / uninstall / readiness / service / smoke /
 * rollback / retry / complete). Returned only by the detail endpoint.
 */
export interface MonitoringOperationEvent {
  id: string;
  level: string;
  stage: string;
  message: string;
  detail?: Record<string, unknown>;
  createdAt: string;
}

/** monitoringOperationResponse() in internal/handler/monitoring_operations.go. */
export interface MonitoringOperation {
  id: string;
  targetType: MonitoringOperationTargetType | string;
  /** Cluster UUID for cluster_stack; the literal "shared" for both shared families. */
  targetKey: string;
  operationType: MonitoringOperationType | string;
  status: MonitoringOperationStatus | string;
  /** Incremented on each reconciler claim, so a retried row shows 2, 3, ... */
  attemptCount: number;
  startedAt?: string | null;
  completedAt?: string | null;
  /** Verbatim reconciler error. Empty string when there is none — never null. */
  errorMessage: string;
  createdAt: string;
  updatedAt: string;
  /** Detail endpoint only. */
  events?: MonitoringOperationEvent[];
}

export const MONITORING_OPERATION_ACTIVE_STATUSES: readonly string[] = ['pending', 'running'];
export const MONITORING_OPERATION_TERMINAL_STATUSES: readonly string[] = [
  'completed',
  'failed',
  'superseded',
];

export function isActiveOperationStatus(status: string | undefined): boolean {
  return !!status && MONITORING_OPERATION_ACTIVE_STATUSES.includes(status);
}

export function isTerminalOperationStatus(status: string | undefined): boolean {
  return !!status && MONITORING_OPERATION_TERMINAL_STATUSES.includes(status);
}

/**
 * Mirrors operationstate.IsRetryable: the backend's RetryOperation endpoint
 * 409s anything that is not `failed` or `superseded`, so the UI must not offer
 * Retry on a completed or in-flight row.
 */
export function isRetryableOperationStatus(status: string | undefined): boolean {
  return status === 'failed' || status === 'superseded';
}

// ─────────────────────────────────────────────────────────────────────
// Lifecycle request bodies (camelCase — see note 1 in the file header)
// ─────────────────────────────────────────────────────────────────────

/**
 * MonitoringStackRequest, internal/handler/monitoring.go. Every field is
 * optional on the wire: the handler defaults releaseName=prometheus,
 * namespace=monitoring, retention=15d, storageSize=50Gi, storageClass=default,
 * scrapeInterval=30s, clusterLabel=cluster_id, clusterLabelValue=<cluster id>,
 * chartVersion=61.3.2, and treats the three tri-state booleans as true when
 * absent.
 */
export interface ClusterStackRequest {
  releaseName?: string;
  namespace?: string;
  retention?: string;
  storageClass?: string;
  storageSize?: string;
  scrapeInterval?: string;
  clusterLabel?: string;
  clusterLabelValue?: string;
  prometheusVersion?: string;
  chartVersion?: string;
  storageConfigId?: string;
  objectStorageSecretName?: string;
  enableGrafana?: boolean;
  enableAlertmanager?: boolean;
  thanosSidecarEnabled?: boolean;
  /** Overrides the backend's defaultAutoRollbackOnFailure policy for this run. */
  autoRollbackOnFailure?: boolean;
}

/**
 * SharedThanosStackRequest. `managementClusterId` and `storageConfigId` are the
 * two the handler genuinely requires — it rejects the request otherwise.
 * managementClusterId may also be supplied as a `?clusterId=` query parameter;
 * this client always sends it in the body.
 */
export interface SharedThanosRequest {
  managementClusterId: string;
  storageConfigId: string;
  namespace?: string;
  releaseName?: string;
  chartVersion?: string;
  objectStorageSecretName?: string;
  queryReplicas?: number;
  storeGatewayReplicas?: number;
  compactorReplicas?: number;
  autoRollbackOnFailure?: boolean;
}

/** SharedAlertmanagerRequest. Only managementClusterId is required. */
export interface SharedAlertmanagerRequest {
  managementClusterId: string;
  namespace?: string;
  releaseName?: string;
  chartVersion?: string;
  replicas?: number;
  storageClass?: string;
  storageSize?: string;
  autoRollbackOnFailure?: boolean;
}

/** SharedGrafanaRequest. Only managementClusterId is required. ClusterIP only. */
export interface SharedGrafanaRequest {
  managementClusterId: string;
  namespace?: string;
  releaseName?: string;
  chartVersion?: string;
  replicas?: number;
  storageClass?: string;
  storageSize?: string;
  ingressHost?: string;
  logDatasourceUrl?: string;
  autoRollbackOnFailure?: boolean;
}

// ─────────────────────────────────────────────────────────────────────
// Preview + status responses
// ─────────────────────────────────────────────────────────────────────

/**
 * All three preview endpoints answer with this exact shape. `values` is the
 * rendered Helm values map, already run through sanitizeMonitoringValues on
 * the server (credentials stripped) — safe to display.
 */
export interface MonitoringStackPreview {
  clusterId: string;
  chart: { repoUrl: string; chartName: string };
  values: Record<string, unknown>;
  desiredSpecHash: string;
  /** True when the change cannot be an in-place upgrade (namespace / release / storage moves). */
  requiresReplace: boolean;
  replaceReasons: string[] | null;
}

/** observeRelease() — the live Helm release next to the recorded desired state. */
export interface ObservedRelease {
  clusterId: string;
  namespace: string;
  releaseName: string;
  observedAt: string;
  /** Helm release status, or the literal "missing" when Helm has no such release. */
  status: string;
  revision?: number;
  error?: string;
}

/**
 * Fields every status endpoint shares. Note that ALL of them are optional
 * except `status`: an unconfigured stack answers with the bare
 * `{"status": "not_configured"}` and nothing else, so screens must not index
 * into this without a guard.
 *
 * `status` is the recorded lifecycle state, NOT the operation state:
 * not_configured | installing | updating | reinstalled | uninstalled |
 * configured | healthy | drifted.
 */
export interface MonitoringStackStatusBase {
  status: string;
  namespace?: string;
  releaseName?: string;
  chartVersion?: string;
  desiredSpecHash?: string;
  observedRelease?: ObservedRelease;
  drifted?: boolean;
  driftReasons?: string[];
  /** Live pod count for the release's instance label, when the k8s requester is wired. */
  pods?: number;
  /**
   * The most recent operation for this target, embedded by the handler
   * (latestMonitoringOperation). This is a convenience projection — the
   * tracker adopts in-flight work from ListOperations instead, because this
   * field is only ever the single newest row and carries no events.
   */
  operation?: MonitoringOperation;
}

/** GET /clusters/{id}/monitoring/stack/status/ */
export interface ClusterStackStatus extends MonitoringStackStatusBase {
  retention?: string;
  thanosSidecarEnabled?: boolean;
  storageConfigId?: string | null;
  objectStorageSecretName?: string;
  storageClass?: string;
  storageSize?: string;
  lastObservedStatus?: string;
  lastObservedRevision?: number;
  lastObservedAt?: string | null;
  lastDriftDetectedAt?: string | null;
  lastHealthyAt?: string | null;
}

/** GET /settings/monitoring/thanos/status/ */
export interface SharedThanosStatus extends MonitoringStackStatusBase {
  managementClusterId?: string;
  storageConfigId?: string;
  objectStorageSecretName?: string;
  queryReplicas?: number;
  storeGatewayReplicas?: number;
  compactorReplicas?: number;
  managedAssetHashes?: Record<string, unknown>;
  alertingAssetHashes?: Record<string, unknown>;
}

/** GET /settings/monitoring/alertmanager/status/ */
export interface SharedAlertmanagerStatus extends MonitoringStackStatusBase {
  managementClusterId?: string;
  replicas?: number;
  storageClass?: string;
  storageSize?: string;
  managedAssetHashes?: Record<string, unknown>;
  alertingAssetHashes?: Record<string, unknown>;
}

/** GET /settings/monitoring/grafana/status/ */
export interface SharedGrafanaStatus extends MonitoringStackStatusBase {
  managementClusterId?: string;
  replicas?: number;
  storageClass?: string;
  storageSize?: string;
  ingressHost?: string;
  logDatasourceUrl?: string;
  grafanaHost?: string;
  authMode?: 'clusterip' | 'proxy' | string;
  autoRollbackOnFailure?: boolean;
  managedAssetHashes?: Record<string, unknown>;
}

// ─────────────────────────────────────────────────────────────────────
// Envelope helpers
// ─────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────
// Per-cluster stack — /clusters/{id}/monitoring/stack/*
// (RBAC: read / read / create / update / update / delete, mounted per route
//  at internal/server/routes_clusters.go:83-88)
//
// ⚠ THESE SIX ENDPOINTS DO NOT WORK AGAINST THE REAL SERVER YET, and the
// clients below are written against the contract they will have once they do.
//
// Every handler resolves its cluster with `chi.URLParam(r, "cluster_id")` —
// internal/handler/monitoring_stack_cluster.go:265 (UninstallStack), :315
// (GetStackStatus), :393 (monitoringStackPayload, backing preview / install /
// upgrade / replace) — while the routes are mounted as `/{id}/monitoring/...`
// and nothing declares `{cluster_id}`. In production the param is always empty:
// status / install / upgrade / replace / uninstall answer >= 400 and preview
// 200s naming no cluster. The one-line fix is `chi.URLParam(r, "id")` at those
// three sites.
//
// This is PINNED as a known-unfixed defect by
// internal/handler/monitoring_stack_test.go:410-465
// (TestClusterStackClusterIDParamIsUnroutable), which passes today and will
// fail the moment the fix lands — at which point delete that test, this note,
// and the on-screen notice in components/monitoring/cluster-stack-page.tsx.
//
// A second consequence, same root cause: RequirePermission also reads
// `cluster_id` and only falls back to `{id}` for rbac.ResourceClusters
// (internal/server/middleware/rbac.go:92-99), so these routes are authorized at
// GLOBAL monitoring scope. cluster-stack-page.tsx asks at global scope to match.
//
// The two SHARED families below are unaffected and work as documented.
// ─────────────────────────────────────────────────────────────────────

const clusterBase = (clusterId: string) =>
  `/clusters/${encodeURIComponent(clusterId)}/monitoring/stack`;

export async function getClusterStackStatus(clusterId: string): Promise<ClusterStackStatus> {
  const res = await api.get<APIResponse<ClusterStackStatus>>(`${clusterBase(clusterId)}/status/`);
  return unwrapData(res.data);
}

export async function previewClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringStackPreview> {
  const res = await api.post<APIResponse<MonitoringStackPreview>>(
    `${clusterBase(clusterId)}/preview/`,
    body,
  );
  return unwrapData(res.data);
}

export async function installClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    `${clusterBase(clusterId)}/install/`,
    body,
  );
  return unwrapData(res.data);
}

/** 409s with a replace_required payload when the change is not upgradeable in place. */
export async function upgradeClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringOperation> {
  const res = await api.put<APIResponse<MonitoringOperation>>(
    `${clusterBase(clusterId)}/upgrade/`,
    body,
  );
  return unwrapData(res.data);
}

export async function replaceClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    `${clusterBase(clusterId)}/replace/`,
    body,
  );
  return unwrapData(res.data);
}

/** Takes no body: the release to remove comes from the persisted cluster config. */
export async function uninstallClusterStack(clusterId: string): Promise<MonitoringOperation> {
  const res = await api.delete<APIResponse<MonitoringOperation>>(
    `${clusterBase(clusterId)}/uninstall/`,
  );
  return unwrapData(res.data);
}

// ─────────────────────────────────────────────────────────────────────
// Shared Thanos — /settings/monitoring/thanos/*
// (RBAC: monitoring:read on status/preview, monitoring:update on the rest,
//  plus the clusters:write token-scope backstop on every mutation)
// ─────────────────────────────────────────────────────────────────────

export async function getSharedThanosStatus(): Promise<SharedThanosStatus> {
  const res = await api.get<APIResponse<SharedThanosStatus>>('/settings/monitoring/thanos/status/');
  return unwrapData(res.data);
}

export async function previewSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringStackPreview> {
  const res = await api.post<APIResponse<MonitoringStackPreview>>(
    '/settings/monitoring/thanos/preview/',
    body,
  );
  return unwrapData(res.data);
}

export async function installSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/thanos/install/',
    body,
  );
  return unwrapData(res.data);
}

export async function upgradeSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringOperation> {
  const res = await api.put<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/thanos/upgrade/',
    body,
  );
  return unwrapData(res.data);
}

export async function replaceSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/thanos/replace/',
    body,
  );
  return unwrapData(res.data);
}

/**
 * Uninstall carries NO body — the driver retargets the zero request at
 * whatever release the persisted metadata names. The cluster comes from
 * `?clusterId=`, falling back server-side to the recorded managementClusterId,
 * so callers that have not got one may omit it.
 */
export async function uninstallSharedThanos(clusterId?: string): Promise<MonitoringOperation> {
  const res = await api.delete<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/thanos/uninstall/',
    clusterId ? { params: { clusterId } } : undefined,
  );
  return unwrapData(res.data);
}

// ─────────────────────────────────────────────────────────────────────
// Shared Alertmanager — /settings/monitoring/alertmanager/*
// ─────────────────────────────────────────────────────────────────────

export async function getSharedAlertmanagerStatus(): Promise<SharedAlertmanagerStatus> {
  const res = await api.get<APIResponse<SharedAlertmanagerStatus>>(
    '/settings/monitoring/alertmanager/status/',
  );
  return unwrapData(res.data);
}

export async function previewSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringStackPreview> {
  const res = await api.post<APIResponse<MonitoringStackPreview>>(
    '/settings/monitoring/alertmanager/preview/',
    body,
  );
  return unwrapData(res.data);
}

export async function installSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/alertmanager/install/',
    body,
  );
  return unwrapData(res.data);
}

export async function upgradeSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringOperation> {
  const res = await api.put<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/alertmanager/upgrade/',
    body,
  );
  return unwrapData(res.data);
}

export async function replaceSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/alertmanager/replace/',
    body,
  );
  return unwrapData(res.data);
}

export async function uninstallSharedAlertmanager(
  clusterId?: string,
): Promise<MonitoringOperation> {
  const res = await api.delete<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/alertmanager/uninstall/',
    clusterId ? { params: { clusterId } } : undefined,
  );
  return unwrapData(res.data);
}

// ─────────────────────────────────────────────────────────────────────
// Shared Grafana — /settings/monitoring/grafana/*
// authMode=proxy after grafana-proxy + ticket bounce. Open button is UI-only.
// ─────────────────────────────────────────────────────────────────────

export async function getSharedGrafanaStatus(): Promise<SharedGrafanaStatus> {
  const res = await api.get<APIResponse<SharedGrafanaStatus>>(
    '/settings/monitoring/grafana/status/',
  );
  return unwrapData(res.data);
}

export async function previewSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringStackPreview> {
  const res = await api.post<APIResponse<MonitoringStackPreview>>(
    '/settings/monitoring/grafana/preview/',
    body,
  );
  return unwrapData(res.data);
}

export async function installSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/grafana/install/',
    body,
  );
  return unwrapData(res.data);
}

export async function upgradeSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringOperation> {
  const res = await api.put<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/grafana/upgrade/',
    body,
  );
  return unwrapData(res.data);
}

export async function replaceSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/grafana/replace/',
    body,
  );
  return unwrapData(res.data);
}

export async function uninstallSharedGrafana(clusterId?: string): Promise<MonitoringOperation> {
  const res = await api.delete<APIResponse<MonitoringOperation>>(
    '/settings/monitoring/grafana/uninstall/',
    clusterId ? { params: { clusterId } } : undefined,
  );
  return unwrapData(res.data);
}

// ─────────────────────────────────────────────────────────────────────
// Operations queue — /settings/monitoring/operations/*
// ─────────────────────────────────────────────────────────────────────

export interface MonitoringOperationListParams {
  targetType?: MonitoringOperationTargetType | string;
  targetKey?: string;
  status?: MonitoringOperationStatus | string;
  limit?: number;
  offset?: number;
}

/**
 * Newest first (ORDER BY created_at DESC), RBAC-filtered in Go — a caller
 * without monitoring:read on a target simply does not see its rows.
 *
 * This is what makes adopt-on-load possible: filtering by
 * (targetType, targetKey) and taking the newest row tells the UI whether work
 * is already in flight for this stack, whether the current user started it or
 * not. The `status` filter accepts ONE value, so callers that want "pending or
 * running" ask unfiltered and inspect the newest row instead of issuing two
 * requests.
 */
export async function listMonitoringOperations(
  params?: MonitoringOperationListParams,
): Promise<MonitoringOperation[]> {
  const res = await api.get<APIResponse<MonitoringOperation[]>>(
    '/settings/monitoring/operations/',
    { params },
  );
  const data = unwrapData(res.data);
  return Array.isArray(data) ? data : [];
}

/** Detail — the only endpoint that returns the operation's stage events. */
export async function getMonitoringOperation(id: string): Promise<MonitoringOperation> {
  const res = await api.get<APIResponse<MonitoringOperation>>(
    `/settings/monitoring/operations/${encodeURIComponent(id)}/`,
  );
  return unwrapData(res.data);
}

/**
 * Re-enqueues a failed or superseded operation IN PLACE: same row, same id,
 * status reset to `pending`, error_message cleared, and the reconciler kicked.
 * Anything else 409s. Because the id is preserved, a tracker that is already
 * following this operation simply keeps following it.
 */
export async function retryMonitoringOperation(id: string): Promise<MonitoringOperation> {
  const res = await api.post<APIResponse<MonitoringOperation>>(
    `/settings/monitoring/operations/${encodeURIComponent(id)}/retry/`,
  );
  return unwrapData(res.data);
}

// ─────────────────────────────────────────────────────────────────────
// Target dispatch
// ─────────────────────────────────────────────────────────────────────

/**
 * Which stack a screen is operating on. The three families are different
 * endpoints with different request shapes, but identical lifecycle semantics —
 * this union lets one hook drive all twelve mutations without twelve hooks.
 */
export type MonitoringStackTarget =
  | { kind: 'cluster'; clusterId: string }
  | { kind: 'thanos' }
  | { kind: 'alertmanager' }
  | { kind: 'grafana' };

export type MonitoringStackRequestBody =
  | ClusterStackRequest
  | SharedThanosRequest
  | SharedAlertmanagerRequest
  | SharedGrafanaRequest;

export type MonitoringStackStatusFor<T extends MonitoringStackTarget> = T extends {
  kind: 'cluster';
}
  ? ClusterStackStatus
  : T extends { kind: 'thanos' }
    ? SharedThanosStatus
    : T extends { kind: 'alertmanager' }
      ? SharedAlertmanagerStatus
      : SharedGrafanaStatus;

/** (targetType, targetKey) as monitoring_operations records them for a target. */
export function operationTargetOf(target: MonitoringStackTarget): {
  targetType: MonitoringOperationTargetType;
  targetKey: string;
} {
  switch (target.kind) {
    case 'cluster':
      return { targetType: 'cluster_stack', targetKey: target.clusterId };
    case 'thanos':
      return { targetType: 'shared_thanos', targetKey: 'shared' };
    case 'alertmanager':
      return { targetType: 'shared_alertmanager', targetKey: 'shared' };
    case 'grafana':
      return { targetType: 'shared_grafana', targetKey: 'shared' };
  }
}

/** Human label for a target, for toasts and headings. */
export function stackTargetLabel(target: MonitoringStackTarget): string {
  switch (target.kind) {
    case 'cluster':
      return 'cluster monitoring stack';
    case 'thanos':
      return 'shared Thanos';
    case 'alertmanager':
      return 'shared Alertmanager';
    case 'grafana':
      return 'shared Grafana';
  }
}

export async function getStackStatus(
  target: MonitoringStackTarget,
): Promise<MonitoringStackStatusBase> {
  switch (target.kind) {
    case 'cluster':
      return getClusterStackStatus(target.clusterId);
    case 'thanos':
      return getSharedThanosStatus();
    case 'alertmanager':
      return getSharedAlertmanagerStatus();
    case 'grafana':
      return getSharedGrafanaStatus();
  }
}

export async function previewStack(
  target: MonitoringStackTarget,
  body: MonitoringStackRequestBody,
): Promise<MonitoringStackPreview> {
  switch (target.kind) {
    case 'cluster':
      return previewClusterStack(target.clusterId, body as ClusterStackRequest);
    case 'thanos':
      return previewSharedThanos(body as SharedThanosRequest);
    case 'alertmanager':
      return previewSharedAlertmanager(body as SharedAlertmanagerRequest);
    case 'grafana':
      return previewSharedGrafana(body as SharedGrafanaRequest);
  }
}

/**
 * Enqueue one lifecycle verb against one target. Returns the 202 receipt —
 * a `pending` MonitoringOperation, not a finished install.
 *
 * `uninstall` ignores the body on every family (per-cluster reads the release
 * from its config row; the shared families read it from backend metadata and
 * accept only the optional cluster id).
 */
export async function runStackLifecycle(
  target: MonitoringStackTarget,
  verb: MonitoringOperationType,
  body?: MonitoringStackRequestBody,
): Promise<MonitoringOperation> {
  switch (target.kind) {
    case 'cluster': {
      const payload = (body ?? {}) as ClusterStackRequest;
      if (verb === 'install') return installClusterStack(target.clusterId, payload);
      if (verb === 'upgrade') return upgradeClusterStack(target.clusterId, payload);
      if (verb === 'replace') return replaceClusterStack(target.clusterId, payload);
      return uninstallClusterStack(target.clusterId);
    }
    case 'thanos': {
      const payload = body as SharedThanosRequest;
      if (verb === 'install') return installSharedThanos(payload);
      if (verb === 'upgrade') return upgradeSharedThanos(payload);
      if (verb === 'replace') return replaceSharedThanos(payload);
      return uninstallSharedThanos(payload?.managementClusterId);
    }
    case 'alertmanager': {
      const payload = body as SharedAlertmanagerRequest;
      if (verb === 'install') return installSharedAlertmanager(payload);
      if (verb === 'upgrade') return upgradeSharedAlertmanager(payload);
      if (verb === 'replace') return replaceSharedAlertmanager(payload);
      return uninstallSharedAlertmanager(payload?.managementClusterId);
    }
    case 'grafana': {
      const payload = body as SharedGrafanaRequest;
      if (verb === 'install') return installSharedGrafana(payload);
      if (verb === 'upgrade') return upgradeSharedGrafana(payload);
      if (verb === 'replace') return replaceSharedGrafana(payload);
      return uninstallSharedGrafana(payload?.managementClusterId);
    }
  }
}

// ─────────────────────────────────────────────────────────────────────
// replace_required (409)
// ─────────────────────────────────────────────────────────────────────

export interface ReplaceRequiredError {
  message: string;
  replaceReasons: string[];
}

/**
 * Upgrade answers 409 when the requested change cannot be applied in place
 * (namespace move, release rename, object-storage or storage-class change).
 *
 * That response is written with RespondJSON, NOT the error helper, so the body
 * is `{ data: { error: "replace_required", message, requiresReplace,
 * replaceReasons } }` — the standard `{ error: { code, message } }` shape the
 * shared `extractApiErrorMessage` helper reads is absent, and it would report
 * axios's generic "Request failed with status code 409" instead of the real
 * message. Screens must run the rejection through this first and offer
 * Replace, only falling back to the generic toast when it returns null.
 */
export function parseReplaceRequiredError(err: unknown): ReplaceRequiredError | null {
  const body = (err as { response?: { status?: number; data?: unknown } })?.response;
  if (!body || body.status !== 409) return null;
  const payload = unwrapData(body.data as Record<string, unknown>) as
    | {
        error?: string;
        message?: string;
        requiresReplace?: boolean;
        replaceReasons?: string[] | null;
      }
    | undefined;
  if (!payload || payload.error !== 'replace_required') return null;
  return {
    message: payload.message ?? 'This change requires a reinstall rather than an in-place upgrade',
    replaceReasons: payload.replaceReasons ?? [],
  };
}
