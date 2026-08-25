/**
 * Generated monitoring-stack lifecycle boundary for cluster Prometheus and
 * the shared Thanos, Alertmanager, Grafana, and Loki families.
 *
 * Backend:
 *   internal/handler/monitoring_stack_cluster.go  (per-cluster kube-prometheus-stack)
 *   internal/handler/monitoring_stack_shared.go   (shared Thanos + Alertmanager + Grafana,
 *                                                  all driven by sharedStackLifecycle)
 *   internal/handler/monitoring_operations.go     (the async queue behind all of them)
 *
 * Two things about these endpoints are not the house default — read before editing.
 *
 * 1. REQUEST BODIES ARE camelCase, not snake_case. The repo convention is that
 *    request bodies are spelled snake_case by hand (there is no request
 *    transport). These handlers are the
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
 * 2. THE MUTATIONS ARE ASYNCHRONOUS. install/upgrade/replace/uninstall return
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
 * Preview response values are raw chart-owned maps. The generated transport
 * preserves their exact keys; never run them through generic key conversion.
 * The legacy transport carve-out remains for compatibility callers until the
 * compatibility barrel is removed.
 */
import * as generated from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type {
  MonitoringOperation,
  MonitoringSizerResponse,
  MonitoringStackPreview,
  OpenAPIComponents,
} from "@/types/openapi.generated";

export type {
  MonitoringOperation,
  MonitoringOperationEvent,
  MonitoringSizerResponse,
  MonitoringSizerVerdict,
  MonitoringStackPreview,
} from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

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
  Schemas["MonitoringOperation"]["status"];

/** monitoring_operations.target_type. One per stack family. */
export type MonitoringOperationTargetType =
  Schemas["MonitoringOperation"]["targetType"];

/** monitoring_operations.operation_type — the four mutating lifecycle verbs. */
export type MonitoringOperationType =
  Schemas["MonitoringOperation"]["operationType"];

/**
 * One row of monitoring_operation_events — the reconciler's stage log
 * (queue / render / install / uninstall / readiness / service / smoke /
 * rollback / retry / complete). Returned only by the detail endpoint.
 */
/** monitoringOperationResponse() in internal/handler/monitoring_operations.go. */

export const MONITORING_OPERATION_ACTIVE_STATUSES: readonly string[] = [
  "pending",
  "running",
];
export const MONITORING_OPERATION_TERMINAL_STATUSES: readonly string[] = [
  "completed",
  "failed",
  "superseded",
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
export function isRetryableOperationStatus(
  status: string | undefined,
): boolean {
  return status === "failed" || status === "superseded";
}

// ─────────────────────────────────────────────────────────────────────
// Lifecycle request bodies (camelCase — see note 1 in the file header)
// ─────────────────────────────────────────────────────────────────────

/**
 * MonitoringStackRequest, internal/handler/monitoring.go. Every field is
 * optional on the wire: the handler defaults releaseName=prometheus,
 * namespace=monitoring, retention=15d, storageSize=50Gi, storageClass=default,
 * scrapeInterval=30s, clusterLabel=cluster_id, clusterLabelValue=<cluster id>,
 * chartVersion=61.3.2, and treats enableAlertmanager / thanosSidecarEnabled as
 * true when absent. Omitted enableGrafana is true unless shared Grafana is
 * healthy and this cluster stack is not_configured, in which case it is false
 * (changelog'd; explicit true/false is unchanged).
 */
export type ClusterStackRequest = Schemas["MonitoringStackRequest"];

/**
 * SharedThanosStackRequest. `managementClusterId` and `storageConfigId` are the
 * two the handler genuinely requires — it rejects the request otherwise.
 * managementClusterId may also be supplied as a `?clusterId=` query parameter;
 * this client always sends it in the body.
 */
export type SharedThanosRequest = Schemas["SharedThanosStackRequest"];

/** SharedAlertmanagerRequest. Only managementClusterId is required. */
export type SharedAlertmanagerRequest =
  Schemas["SharedAlertmanagerStackRequest"];

/** SharedGrafanaRequest. Only managementClusterId is required. ClusterIP only. */
export type SharedGrafanaRequest = Schemas["SharedGrafanaStackRequest"];

/** SharedLokiRequest. managementClusterId, storageConfigId, ingestHostname required. ClusterIP only. */
export type SharedLokiRequest = Schemas["SharedLokiStackRequest"];

// ─────────────────────────────────────────────────────────────────────
// Preview + status responses
// ─────────────────────────────────────────────────────────────────────

/**
 * All three preview endpoints answer with this exact shape. `values` is the
 * rendered Helm values map, already run through sanitizeMonitoringValues on
 * the server (credentials stripped) — safe to display.
 */
/** observeRelease() — the live Helm release next to the recorded desired state. */
export type ObservedRelease = Schemas["MonitoringObservedRelease"];

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
export type MonitoringStackStatusBase = Schemas["MonitoringStackStatus"];

/** GET /clusters/{id}/monitoring/stack/status/ */
export type ClusterStackStatus = Schemas["MonitoringStackStatus"];

/** GET /settings/monitoring/thanos/status/ */
export type SharedThanosStatus = Schemas["MonitoringStackStatus"];

/** GET /settings/monitoring/alertmanager/status/ */
export type SharedAlertmanagerStatus = Schemas["MonitoringStackStatus"];

/** GET /settings/monitoring/grafana/status/ */
export type SharedGrafanaStatus = Schemas["MonitoringStackStatus"];

/** GET /settings/monitoring/loki/status/ */
export type SharedLokiStatus = Schemas["MonitoringStackStatus"];
// ─────────────────────────────────────────────────────────────────────
// Per-cluster stack — /clusters/{id}/monitoring/stack/*
// (RBAC: read / read / create / update / update / delete, mounted per route
//  at internal/server/routes_clusters.go:83-88)
// ─────────────────────────────────────────────────────────────────────

export async function getClusterStackStatus(
  clusterId: string,
): Promise<ClusterStackStatus> {
  return requireData(
    await generated.getClustersByIdMonitoringStackStatus({
      path: { id: clusterId },
    }),
    "getClusterStackStatus",
  );
}

export async function previewClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postClustersByIdMonitoringStackPreview({
      path: { id: clusterId },
      body,
    }),
    "previewClusterStack",
  );
}

export async function installClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postClustersByIdMonitoringStackInstall({
      path: { id: clusterId },
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "installClusterStack",
  );
}

/** 409s with a replace_required payload when the change is not upgradeable in place. */
export async function upgradeClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringOperation> {
  return requireData(
    await generated.putClustersByIdMonitoringStackUpgrade({
      path: { id: clusterId },
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "upgradeClusterStack",
  );
}

export async function replaceClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postClustersByIdMonitoringStackReplace({
      path: { id: clusterId },
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "replaceClusterStack",
  );
}

/** Takes no body: the release to remove comes from the persisted cluster config. */
export async function uninstallClusterStack(
  clusterId: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.deleteClustersByIdMonitoringStackUninstall({
      path: { id: clusterId },
      headerParams: idempotencyHeaderParams(),
    }),
    "uninstallClusterStack",
  );
}

// ─────────────────────────────────────────────────────────────────────
// Shared Thanos — /settings/monitoring/thanos/*
// (RBAC: monitoring:read on status/preview, monitoring:update on the rest,
//  plus the clusters:write token-scope backstop on every mutation)
// ─────────────────────────────────────────────────────────────────────

export async function getSharedThanosStatus(): Promise<SharedThanosStatus> {
  return requireData(
    await generated.getSettingsMonitoringThanosStatus(),
    "getSharedThanosStatus",
  );
}

export async function previewSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringThanosPreview({ body }),
    "previewSharedThanos",
  );
}

export async function installSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringThanosInstall({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "installSharedThanos",
  );
}

export async function upgradeSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.putSettingsMonitoringThanosUpgrade({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "upgradeSharedThanos",
  );
}

export async function replaceSharedThanos(
  body: SharedThanosRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringThanosReplace({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "replaceSharedThanos",
  );
}

/**
 * Uninstall carries NO body — the driver retargets the zero request at
 * whatever release the persisted metadata names. The cluster comes from
 * `?clusterId=`, falling back server-side to the recorded managementClusterId,
 * so callers that have not got one may omit it.
 */
export async function uninstallSharedThanos(
  clusterId?: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.deleteSettingsMonitoringThanosUninstall({
      query: clusterId ? { clusterId } : undefined,
      headerParams: idempotencyHeaderParams(),
    }),
    "uninstallSharedThanos",
  );
}

// ─────────────────────────────────────────────────────────────────────
// Shared Alertmanager — /settings/monitoring/alertmanager/*
// ─────────────────────────────────────────────────────────────────────

export async function getSharedAlertmanagerStatus(): Promise<SharedAlertmanagerStatus> {
  return requireData(
    await generated.getSettingsMonitoringAlertmanagerStatus(),
    "getSharedAlertmanagerStatus",
  );
}

export async function previewSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringAlertmanagerPreview({ body }),
    "previewSharedAlertmanager",
  );
}

export async function installSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringAlertmanagerInstall({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "installSharedAlertmanager",
  );
}

export async function upgradeSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.putSettingsMonitoringAlertmanagerUpgrade({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "upgradeSharedAlertmanager",
  );
}

export async function replaceSharedAlertmanager(
  body: SharedAlertmanagerRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringAlertmanagerReplace({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "replaceSharedAlertmanager",
  );
}

export async function uninstallSharedAlertmanager(
  clusterId?: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.deleteSettingsMonitoringAlertmanagerUninstall({
      query: clusterId ? { clusterId } : undefined,
      headerParams: idempotencyHeaderParams(),
    }),
    "uninstallSharedAlertmanager",
  );
}

// ─────────────────────────────────────────────────────────────────────
// Shared Grafana — /settings/monitoring/grafana/*
// authMode=proxy after grafana-proxy + ticket bounce. Open button is UI-only.
// ─────────────────────────────────────────────────────────────────────

export async function getSharedGrafanaStatus(): Promise<SharedGrafanaStatus> {
  return requireData(
    await generated.getSettingsMonitoringGrafanaStatus(),
    "getSharedGrafanaStatus",
  );
}

export async function previewSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringGrafanaPreview({ body }),
    "previewSharedGrafana",
  );
}

export async function installSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringGrafanaInstall({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "installSharedGrafana",
  );
}

export async function upgradeSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.putSettingsMonitoringGrafanaUpgrade({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "upgradeSharedGrafana",
  );
}

export async function replaceSharedGrafana(
  body: SharedGrafanaRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringGrafanaReplace({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "replaceSharedGrafana",
  );
}

export async function uninstallSharedGrafana(
  clusterId?: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.deleteSettingsMonitoringGrafanaUninstall({
      query: clusterId ? { clusterId } : undefined,
      headerParams: idempotencyHeaderParams(),
    }),
    "uninstallSharedGrafana",
  );
}

export async function getMonitoringSizer(): Promise<MonitoringSizerResponse> {
  return requireData(
    await generated.getSettingsMonitoringSizer(),
    "getMonitoringSizer",
  );
}

// ─────────────────────────────────────────────────────────────────────
// Shared Loki — /settings/monitoring/loki/*
// Feature-gated: feature.hosted_loki must be exactly true. ClusterIP only.
// ─────────────────────────────────────────────────────────────────────

export async function getSharedLokiStatus(): Promise<SharedLokiStatus> {
  return requireData(
    await generated.getSettingsMonitoringLokiStatus(),
    "getSharedLokiStatus",
  );
}

export async function previewSharedLoki(
  body: SharedLokiRequest,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringLokiPreview({ body }),
    "previewSharedLoki",
  );
}

export async function installSharedLoki(
  body: SharedLokiRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringLokiInstall({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "installSharedLoki",
  );
}

export async function upgradeSharedLoki(
  body: SharedLokiRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.putSettingsMonitoringLokiUpgrade({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "upgradeSharedLoki",
  );
}

export async function replaceSharedLoki(
  body: SharedLokiRequest,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringLokiReplace({
      headerParams: idempotencyHeaderParams(),
      body,
    }),
    "replaceSharedLoki",
  );
}

export async function uninstallSharedLoki(
  clusterId?: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.deleteSettingsMonitoringLokiUninstall({
      query: clusterId ? { clusterId } : undefined,
      headerParams: idempotencyHeaderParams(),
    }),
    "uninstallSharedLoki",
  );
}

// ─────────────────────────────────────────────────────────────────────
// Operations queue — /settings/monitoring/operations/*
// ─────────────────────────────────────────────────────────────────────

export interface MonitoringOperationListParams {
  targetType?: MonitoringOperationTargetType;
  targetKey?: string;
  status?: MonitoringOperationStatus;
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
  const response = await generated.getSettingsMonitoringOperations({
    query: params,
  });
  return response.data ?? [];
}

/** Detail — the only endpoint that returns the operation's stage events. */
export async function getMonitoringOperation(
  id: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.getSettingsMonitoringOperationsById({ path: { id } }),
    "getMonitoringOperation",
  );
}

/**
 * Re-enqueues a failed or superseded operation IN PLACE: same row, same id,
 * status reset to `pending`, error_message cleared, and the reconciler kicked.
 * Anything else 409s. Because the id is preserved, a tracker that is already
 * following this operation simply keeps following it.
 */
export async function retryMonitoringOperation(
  id: string,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.postSettingsMonitoringOperationsByIdRetry({
      path: { id },
      headerParams: idempotencyHeaderParams(),
    }),
    "retryMonitoringOperation",
  );
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
  | { kind: "cluster"; clusterId: string }
  | { kind: "thanos" }
  | { kind: "alertmanager" }
  | { kind: "grafana" }
  | { kind: "loki" };

export type MonitoringStackRequestBody =
  | ClusterStackRequest
  | SharedThanosRequest
  | SharedAlertmanagerRequest
  | SharedGrafanaRequest
  | SharedLokiRequest;

export type MonitoringStackStatusFor<T extends MonitoringStackTarget> =
  T extends {
    kind: "cluster";
  }
    ? ClusterStackStatus
    : T extends { kind: "thanos" }
      ? SharedThanosStatus
      : T extends { kind: "alertmanager" }
        ? SharedAlertmanagerStatus
        : T extends { kind: "grafana" }
          ? SharedGrafanaStatus
          : SharedLokiStatus;

/** (targetType, targetKey) as monitoring_operations records them for a target. */
export function operationTargetOf(target: MonitoringStackTarget): {
  targetType: MonitoringOperationTargetType;
  targetKey: string;
} {
  switch (target.kind) {
    case "cluster":
      return { targetType: "cluster_stack", targetKey: target.clusterId };
    case "thanos":
      return { targetType: "shared_thanos", targetKey: "shared" };
    case "alertmanager":
      return { targetType: "shared_alertmanager", targetKey: "shared" };
    case "grafana":
      return { targetType: "shared_grafana", targetKey: "shared" };
    case "loki":
      return { targetType: "shared_loki", targetKey: "shared" };
  }
}

/** Human label for a target, for toasts and headings. */
export function stackTargetLabel(target: MonitoringStackTarget): string {
  switch (target.kind) {
    case "cluster":
      return "cluster monitoring stack";
    case "thanos":
      return "shared Thanos";
    case "alertmanager":
      return "shared Alertmanager";
    case "grafana":
      return "shared Grafana";
    case "loki":
      return "shared Loki";
  }
}

export async function getStackStatus(
  target: MonitoringStackTarget,
): Promise<MonitoringStackStatusBase> {
  switch (target.kind) {
    case "cluster":
      return getClusterStackStatus(target.clusterId);
    case "thanos":
      return getSharedThanosStatus();
    case "alertmanager":
      return getSharedAlertmanagerStatus();
    case "grafana":
      return getSharedGrafanaStatus();
    case "loki":
      return getSharedLokiStatus();
  }
}

export async function previewStack(
  target: MonitoringStackTarget,
  body: MonitoringStackRequestBody,
): Promise<MonitoringStackPreview> {
  switch (target.kind) {
    case "cluster":
      return previewClusterStack(target.clusterId, body as ClusterStackRequest);
    case "thanos":
      return previewSharedThanos(body as SharedThanosRequest);
    case "alertmanager":
      return previewSharedAlertmanager(body as SharedAlertmanagerRequest);
    case "grafana":
      return previewSharedGrafana(body as SharedGrafanaRequest);
    case "loki":
      return previewSharedLoki(body as SharedLokiRequest);
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
    case "cluster": {
      const payload = (body ?? {}) as ClusterStackRequest;
      if (verb === "install")
        return installClusterStack(target.clusterId, payload);
      if (verb === "upgrade")
        return upgradeClusterStack(target.clusterId, payload);
      if (verb === "replace")
        return replaceClusterStack(target.clusterId, payload);
      return uninstallClusterStack(target.clusterId);
    }
    case "thanos": {
      const payload = body as SharedThanosRequest;
      if (verb === "install") return installSharedThanos(payload);
      if (verb === "upgrade") return upgradeSharedThanos(payload);
      if (verb === "replace") return replaceSharedThanos(payload);
      return uninstallSharedThanos(payload?.managementClusterId);
    }
    case "alertmanager": {
      const payload = body as SharedAlertmanagerRequest;
      if (verb === "install") return installSharedAlertmanager(payload);
      if (verb === "upgrade") return upgradeSharedAlertmanager(payload);
      if (verb === "replace") return replaceSharedAlertmanager(payload);
      return uninstallSharedAlertmanager(payload?.managementClusterId);
    }
    case "grafana": {
      const payload = body as SharedGrafanaRequest;
      if (verb === "install") return installSharedGrafana(payload);
      if (verb === "upgrade") return upgradeSharedGrafana(payload);
      if (verb === "replace") return replaceSharedGrafana(payload);
      return uninstallSharedGrafana(payload?.managementClusterId);
    }
    case "loki": {
      const payload = body as SharedLokiRequest;
      if (verb === "install") return installSharedLoki(payload);
      if (verb === "upgrade") return upgradeSharedLoki(payload);
      if (verb === "replace") return replaceSharedLoki(payload);
      return uninstallSharedLoki(payload?.managementClusterId);
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
export function parseReplaceRequiredError(
  err: unknown,
): ReplaceRequiredError | null {
  const body = (err as { response?: { status?: number; data?: unknown } })
    ?.response;
  if (!body || body.status !== 409) return null;
  const responseData = body.data as Record<string, unknown> | undefined;
  const payload = (
    responseData && "data" in responseData ? responseData.data : responseData
  ) as
    | {
        error?: string;
        message?: string;
        requiresReplace?: boolean;
        replaceReasons?: string[] | null;
      }
    | undefined;
  if (!payload || payload.error !== "replace_required") return null;
  return {
    message:
      payload.message ??
      "This change requires a reinstall rather than an in-place upgrade",
    replaceReasons: payload.replaceReasons ?? [],
  };
}
