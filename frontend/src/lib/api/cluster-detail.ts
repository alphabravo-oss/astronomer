/**
 * Cluster-detail API client — Velero snapshots/schedules, private registries,
 * and cluster-template binding. Backend endpoints live under
 * `/api/v1/clusters/{cluster_id}/…`. Generated responses retain wire casing;
 * this module owns the explicit mappings into its camelCase view types.
 *
 * Re-exported from ../api.ts via `export * from './api/cluster-detail'`.
 */

import * as generated from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import type { OperationSnapshot } from "@/lib/api/operation-polling";
import type { OpenAPIComponents } from "@/types/openapi.generated";

// ============================================================
// Snapshots (Velero)
// ============================================================

export type SnapshotPhase =
  | "New"
  | "InProgress"
  | "Completed"
  | "PartiallyFailed"
  | "Failed"
  | "FailedValidation"
  | "Deleting"
  | string;

export interface SnapshotSpec {
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  includedResources?: string[];
  excludedResources?: string[];
  snapshotVolumes?: boolean;
  ttl?: string;
  storageLocation?: string;
  labelSelector?: string;
  volumeSnapshotLocations?: string[];
}

export interface Snapshot {
  id: string;
  name: string;
  source: "adhoc" | "schedule";
  scheduleId?: string;
  scheduleName?: string;
  phase: SnapshotPhase;
  spec: SnapshotSpec;
  startTimestamp?: string;
  completionTimestamp?: string;
  expiration?: string;
  warnings?: number;
  errors?: number;
  validationErrors?: string[];
  createdAt: string;
}

export interface SnapshotSchedule {
  id: string;
  name: string;
  cron: string;
  enabled: boolean;
  spec: SnapshotSpec;
  lastRun?: string;
  nextRun?: string;
  createdAt: string;
  updatedAt: string;
}

export interface VeleroBSLSummary {
  name: string;
  provider: string;
  default: boolean;
  phase: string;
  bucket: string;
}

export interface VeleroStatus {
  installed: boolean;
  namespace?: string;
  storageReady: boolean;
  storageLocations?: VeleroBSLSummary[];
  reason?: string;
}

type SnapshotWire = OpenAPIComponents["schemas"]["SnapshotResponse"];
type SnapshotScheduleWire =
  OpenAPIComponents["schemas"]["SnapshotScheduleResponse"];
type SnapshotRestoreWire =
  OpenAPIComponents["schemas"]["SnapshotRestoreResponse"];
type VeleroStatusWire = OpenAPIComponents["schemas"]["VeleroStatusResponse"];

function requiredClusterDetailData<T>(
  envelope: { data?: T },
  operation: string,
): T {
  if (envelope.data === undefined) {
    throw new Error(`${operation} returned an empty data envelope`);
  }
  return envelope.data;
}

function mapSnapshotSpec(
  wire: OpenAPIComponents["schemas"]["SnapshotSpec"] | undefined,
): SnapshotSpec {
  return {
    includedNamespaces: wire?.includedNamespaces,
    excludedNamespaces: wire?.excludedNamespaces,
    includedResources: wire?.includedResources,
    excludedResources: wire?.excludedResources,
    snapshotVolumes: wire?.snapshotVolumes ?? undefined,
    ttl: wire?.ttl,
    storageLocation: wire?.storageLocation,
    labelSelector: wire?.labelSelector,
    volumeSnapshotLocations: wire?.volumeSnapshotLocations,
  };
}

function mapSnapshot(wire: SnapshotWire): Snapshot {
  if (!wire.id || !wire.velero_name || !wire.phase || !wire.created_at) {
    throw new Error("Snapshot response is missing required identity fields");
  }
  return {
    id: wire.id,
    name: wire.velero_name,
    source: wire.source === "schedule" ? "schedule" : "adhoc",
    phase: wire.phase,
    spec: mapSnapshotSpec(wire.spec),
    startTimestamp: wire.start_time ?? undefined,
    completionTimestamp: wire.completion_time ?? undefined,
    expiration: wire.expires_at ?? undefined,
    warnings: wire.warnings_count,
    errors: wire.errors_count,
    createdAt: wire.created_at,
  };
}

function mapSnapshotSchedule(wire: SnapshotScheduleWire): SnapshotSchedule {
  if (
    !wire.id ||
    !wire.name ||
    !wire.cron_schedule ||
    wire.enabled === undefined ||
    !wire.created_at ||
    !wire.updated_at
  ) {
    throw new Error("Snapshot schedule response is incomplete");
  }
  return {
    id: wire.id,
    name: wire.name,
    cron: wire.cron_schedule,
    enabled: wire.enabled,
    spec: mapSnapshotSpec(wire.spec),
    lastRun: wire.last_run_at ?? undefined,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapSnapshotRestore(wire: SnapshotRestoreWire): SnapshotRestore {
  if (
    !wire.id ||
    !wire.snapshot_id ||
    !wire.target_cluster_id ||
    !wire.velero_name ||
    !wire.phase
  ) {
    throw new Error("Snapshot restore response is incomplete");
  }
  return {
    id: wire.id,
    name: wire.velero_name,
    snapshotId: wire.snapshot_id,
    targetClusterId: wire.target_cluster_id,
    phase: wire.phase,
    startTimestamp: wire.start_time ?? undefined,
    completionTimestamp: wire.completion_time ?? undefined,
    errors: wire.errors_count,
    warnings: wire.warnings_count,
  };
}

function mapVeleroStatus(wire: VeleroStatusWire): VeleroStatus {
  return {
    installed: wire.installed ?? false,
    namespace: wire.namespace,
    storageReady: wire.storage_ready ?? false,
    storageLocations: wire.storage_locations?.map((location) => ({
      name: location.name ?? "",
      provider: location.provider ?? "",
      default: location.default ?? false,
      phase: location.phase ?? "",
      bucket: location.bucket ?? "",
    })),
    reason: wire.reason,
  };
}

export async function getVeleroStatus(
  clusterId: string,
  signal?: AbortSignal,
): Promise<VeleroStatus> {
  const wire = await generated.getClustersByClusterIdVeleroStatus({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapVeleroStatus(requiredClusterDetailData(wire, "Velero status"));
}

export async function listSnapshots(
  clusterId: string,
  signal?: AbortSignal,
): Promise<Snapshot[]> {
  const wire = await generated.getClustersByClusterIdSnapshots({
    path: { cluster_id: clusterId },
    signal,
  });
  return (requiredClusterDetailData(wire, "Snapshot list").items ?? []).map(
    mapSnapshot,
  );
}

export async function createSnapshot(
  clusterId: string,
  body: { spec: SnapshotSpec },
  signal?: AbortSignal,
): Promise<Snapshot> {
  const wire = await generated.postClustersByClusterIdSnapshots({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body: body.spec,
    signal,
  });
  return mapSnapshot(requiredClusterDetailData(wire, "Snapshot create"));
}

export async function deleteSnapshot(
  clusterId: string,
  snapshotId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdSnapshotsById({
    path: { cluster_id: clusterId, id: snapshotId },
    signal,
  });
}

export interface RestoreSnapshotRequest {
  target_cluster_id?: string;
  spec?: {
    includedNamespaces?: string[];
    excludedNamespaces?: string[];
    namespaceMapping?: Record<string, string>;
    restorePVs?: boolean;
  };
}

export interface SnapshotRestore {
  id: string;
  name: string;
  snapshotId: string;
  targetClusterId: string;
  phase: string;
  startTimestamp?: string;
  completionTimestamp?: string;
  errors?: number;
  warnings?: number;
}

export async function restoreSnapshot(
  clusterId: string,
  snapshotId: string,
  body: RestoreSnapshotRequest,
  signal?: AbortSignal,
): Promise<SnapshotRestore> {
  const wire = await generated.postClustersByClusterIdSnapshotsByIdRestore({
    path: { cluster_id: clusterId, id: snapshotId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body,
    signal,
  });
  return mapSnapshotRestore(
    requiredClusterDetailData(wire, "Snapshot restore"),
  );
}

export async function listSnapshotSchedules(
  clusterId: string,
  signal?: AbortSignal,
): Promise<SnapshotSchedule[]> {
  const wire = await generated.getClustersByClusterIdSnapshotSchedules({
    path: { cluster_id: clusterId },
    signal,
  });
  return (
    requiredClusterDetailData(wire, "Snapshot schedule list").items ?? []
  ).map(mapSnapshotSchedule);
}

export async function createSnapshotSchedule(
  clusterId: string,
  body: { name: string; cron: string; enabled?: boolean; spec: SnapshotSpec },
  signal?: AbortSignal,
): Promise<SnapshotSchedule> {
  const wire = await generated.postClustersByClusterIdSnapshotSchedules({
    path: { cluster_id: clusterId },
    body: {
      name: body.name,
      cron_schedule: body.cron,
      enabled: body.enabled,
      spec: body.spec,
    },
    signal,
  });
  return mapSnapshotSchedule(
    requiredClusterDetailData(wire, "Snapshot schedule create"),
  );
}

export async function updateSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
  body: Partial<{
    name: string;
    cron: string;
    enabled: boolean;
    spec: SnapshotSpec;
  }>,
  signal?: AbortSignal,
): Promise<SnapshotSchedule> {
  const wire = await generated.putClustersByClusterIdSnapshotSchedulesById({
    path: { cluster_id: clusterId, id: scheduleId },
    body: {
      name: body.name,
      cron_schedule: body.cron,
      enabled: body.enabled,
      spec: body.spec,
    },
    signal,
  });
  return mapSnapshotSchedule(
    requiredClusterDetailData(wire, "Snapshot schedule update"),
  );
}

export async function deleteSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdSnapshotSchedulesById({
    path: { cluster_id: clusterId, id: scheduleId },
    signal,
  });
}
// Cluster registries (private image-pull credentials)
// ============================================================

export interface ClusterRegistry {
  id: string;
  registryUrl: string;
  username: string;
  namespaces: string[];
  secretName: string;
  injectDefaultSa: boolean;
  lastAppliedAt?: string;
  lastApplyError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateRegistryRequest {
  registry_url: string;
  username: string;
  password: string;
  namespaces?: string[];
  secret_name?: string;
  inject_default_sa?: boolean;
}

export interface UpdateRegistryRequest {
  registry_url?: string;
  username?: string;
  /** Omit to preserve the existing password. */
  password?: string;
  namespaces?: string[];
  secret_name?: string;
  inject_default_sa?: boolean;
}

export interface RegistryTestResult {
  ok: boolean;
  statusCode?: number;
  message?: string;
  /** Retained for view compatibility; this endpoint currently reports statusCode instead. */
  latencyMs?: number;
}

function mapClusterRegistry(wire: Record<string, unknown>): ClusterRegistry {
  return {
    id: String(wire.id ?? ""),
    registryUrl: String(wire.private_registry_url ?? ""),
    username: String(wire.registry_username ?? ""),
    namespaces: Array.isArray(wire.namespaces) ? (wire.namespaces as string[]) : [],
    secretName: String(wire.secret_name ?? ""),
    injectDefaultSa: Boolean(wire.inject_default_sa),
    lastAppliedAt: wire.last_applied_at ? String(wire.last_applied_at) : undefined,
    lastApplyError: wire.last_apply_error ? String(wire.last_apply_error) : undefined,
    createdAt: String(wire.created_at ?? ""),
    updatedAt: String(wire.updated_at ?? ""),
  };
}

export async function listClusterRegistries(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterRegistry[]> {
  const wire = await generated.getClustersByClusterIdRegistries({
    path: { cluster_id: clusterId },
    signal,
  });
  return (wire.data?.items ?? []).map(mapClusterRegistry);
}

export async function createClusterRegistry(
  clusterId: string,
  body: CreateRegistryRequest,
  signal?: AbortSignal,
): Promise<ClusterRegistry> {
  const wire = await generated.postClustersByClusterIdRegistries({
    path: { cluster_id: clusterId },
    body: {
      private_registry_url: body.registry_url,
      registry_username: body.username,
      registry_password: body.password,
      namespaces: body.namespaces,
      secret_name: body.secret_name,
      inject_default_sa: body.inject_default_sa,
    },
    signal,
  });
  return mapClusterRegistry(requiredClusterDetailData(wire, "Registry create"));
}

export async function updateClusterRegistry(
  clusterId: string,
  registryId: string,
  body: UpdateRegistryRequest,
  signal?: AbortSignal,
): Promise<ClusterRegistry> {
  const wire = await generated.putClustersByClusterIdRegistriesById({
    path: { cluster_id: clusterId, id: registryId },
    body: {
      private_registry_url: body.registry_url,
      registry_username: body.username,
      registry_password: body.password,
      namespaces: body.namespaces,
      secret_name: body.secret_name,
      inject_default_sa: body.inject_default_sa,
    },
    signal,
  });
  return mapClusterRegistry(requiredClusterDetailData(wire, "Registry update"));
}

export async function deleteClusterRegistry(
  clusterId: string,
  registryId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdRegistriesById({
    path: { cluster_id: clusterId, id: registryId },
    signal,
  });
}

export async function testClusterRegistry(
  clusterId: string,
  registryId: string,
  signal?: AbortSignal,
): Promise<RegistryTestResult> {
  const wire = await generated.postClustersByClusterIdRegistriesByIdTest({
    path: { cluster_id: clusterId, id: registryId },
    signal,
  });
  const data = requiredClusterDetailData(wire, "Registry test");
  return {
    ok: Boolean(data.ok),
    statusCode: data.status_code == null ? undefined : Number(data.status_code),
    message: data.message == null ? undefined : String(data.message),
  };
}

// ============================================================
// Cluster template binding
// ============================================================

export type ClusterTemplateStatus =
  "pending" | "applying" | "applied" | "failed" | string;

/**
 * Shape returned by `GET /clusters/{id}/template/` — the *binding* between a
 * cluster and the template it has applied. The template type itself (with
 * the editable spec) lives in ./project-detail.ts; importing it here would
 * create a circular re-export, so callers that need both pull each from its
 * source module.
 */
export interface ClusterTemplateBinding {
  templateId: string;
  templateName: string;
  templateDisplayName: string;
  status: ClusterTemplateStatus;
  appliedAt?: string;
  lastError?: string;
  spec: unknown;
}

function mapClusterTemplateBinding(
  wire: Record<string, unknown>,
): ClusterTemplateBinding {
  return {
    templateId: String(wire.template_id ?? ""),
    templateName: String(wire.template_name ?? ""),
    templateDisplayName: String(wire.template_name ?? ""),
    status: String(wire.status ?? ""),
    appliedAt: wire.applied_at ? String(wire.applied_at) : undefined,
    lastError: wire.last_error ? String(wire.last_error) : undefined,
    spec: wire.spec_snapshot,
  };
}

export async function getClusterTemplateBinding(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterTemplateBinding | null> {
  try {
    const wire = await generated.getClustersByClusterIdTemplate({
      path: { cluster_id: clusterId },
      signal,
    });
    return wire.data ? mapClusterTemplateBinding(wire.data) : null;
  } catch (err) {
    // 404 — no template bound. Anything else surfaces to the caller.
    const status = (err as { response?: { status?: number } })?.response
      ?.status;
    if (status === 404) return null;
    throw err;
  }
}

export async function bindClusterTemplate(
  clusterId: string,
  body: { template_id: string },
  signal?: AbortSignal,
): Promise<ClusterTemplateBinding> {
  const wire = await generated.postClustersByClusterIdTemplate({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body,
    signal,
  });
  return mapClusterTemplateBinding(requiredClusterDetailData(wire, "Template bind"));
}

export async function reapplyClusterTemplate(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterTemplateBinding> {
  const wire = await generated.postClustersByClusterIdTemplateReapply({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    signal,
  });
  return mapClusterTemplateBinding(requiredClusterDetailData(wire, "Template reapply"));
}

export async function detachClusterTemplate(
  clusterId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdTemplate({
    path: { cluster_id: clusterId },
    signal,
  });
}

// ============================================================
// Image vulnerability scans (Sprint 062)
// ============================================================

/**
 * Aggregate severity counts for a cluster's image scans.
 * Mirrors the AggregateClusterVulnerabilitiesRow projection on the
 * server; values are sums across every report row, not point-in-time
 * scanner state.
 */
export interface ImageVulnSummary {
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
  reportCount: number;
  lastScannedAt: string | null;
}

export interface ImageVulnReport {
  id: string;
  clusterId: string;
  reportName: string;
  namespace: string;
  workloadKind: string;
  workloadName: string;
  containerName: string;
  imageRegistry: string;
  imageRepo: string;
  imageTag: string;
  imageDigest: string;
  scanner: string;
  scannerVersion: string;
  criticalCount: number;
  highCount: number;
  mediumCount: number;
  lowCount: number;
  unknownCount: number;
  scannedAt: string;
  createdAt: string;
  updatedAt: string;
}

export type CVESeverity =
  "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN" | string;

export interface CVERow {
  id: string;
  reportId: string;
  vulnerabilityId: string;
  severity: CVESeverity;
  pkgName: string;
  installedVersion: string;
  fixedVersion: string;
  primaryLink: string;
  cvssScore: number | null;
  title: string;
  description: string;
}

export interface ImageVulnReportDetail {
  report: ImageVulnReport;
  vulnerabilities: CVERow[];
  vulnerabilityTotal: number;
  severityFilter: string;
  limit: number;
  offset: number;
}

type ImageVulnReportWire = OpenAPIComponents["schemas"]["ImageVulnerabilityReport"];

function mapImageVulnReport(raw: ImageVulnReportWire): ImageVulnReport {
  return {
    id: raw.id,
    clusterId: raw.cluster_id,
    reportName: raw.report_name,
    namespace: raw.namespace,
    workloadKind: raw.workload_kind,
    workloadName: raw.workload_name,
    containerName: raw.container_name,
    imageRegistry: raw.image_registry,
    imageRepo: raw.image_repo,
    imageTag: raw.image_tag,
    imageDigest: raw.image_digest,
    scanner: raw.scanner,
    scannerVersion: raw.scanner_version,
    criticalCount: raw.critical_count,
    highCount: raw.high_count,
    mediumCount: raw.medium_count,
    lowCount: raw.low_count,
    unknownCount: raw.unknown_count,
    scannedAt: raw.scanned_at,
    createdAt: raw.created_at,
    updatedAt: raw.updated_at,
  };
}

export interface ImageVulnRescanResult extends OperationSnapshot {
  clusterId: string;
  requestedAt: string;
  operationUrl: string;
}

export async function getImageVulnSummary(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ImageVulnSummary> {
  // The Go backend returns snake_case (report_count, last_scanned_at);
  // the TS interface uses camelCase for everywhere-else convenience.
  // Map at the boundary so a stray `summary.lastScannedAt` doesn't
  // silently read undefined → "Invalid Date" + a "0 reports" tile,
  // which is exactly the bug operators were seeing on this page.
  const wire = await generated.getClustersByIdVulnerabilitiesSummary({
    path: { id: clusterId },
    signal,
  });
  const raw = requiredClusterDetailData(wire, "Vulnerability summary");
  return {
    critical: Number(raw.critical ?? 0),
    high: Number(raw.high ?? 0),
    medium: Number(raw.medium ?? 0),
    low: Number(raw.low ?? 0),
    unknown: Number(raw.unknown ?? 0),
    reportCount: Number(raw.report_count ?? 0),
    lastScannedAt: raw.last_scanned_at ?? null,
  };
}

export async function listVulnerableImages(
  clusterId: string,
  opts: { namespace?: string; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<{ items: ImageVulnReport[]; total: number }> {
  const wire = await generated.getClustersByIdVulnerabilitiesImages({
    path: { id: clusterId },
    query: {
      namespace: opts.namespace,
      limit: opts.limit,
      offset: opts.offset,
    },
    signal,
  });
  // Snake → camel at the boundary. The Go side returns
  // critical_count / scanned_at / image_repo etc.; the TS interface
  // calls them criticalCount / scannedAt / imageRepo. Without this
  // mapping, every property read on a row is undefined → table shows
  // 0 for every count column and "Invalid Date" for scannedAt.
  const items = (wire.data ?? []).map((raw) => mapImageVulnReport({
    id: raw.id ?? "",
    cluster_id: raw.cluster_id ?? "",
    report_name: raw.report_name ?? "",
    namespace: raw.namespace ?? "",
    workload_kind: raw.workload_kind ?? "",
    workload_name: raw.workload_name ?? "",
    container_name: raw.container_name ?? "",
    image_registry: raw.image_registry ?? "",
    image_repo: raw.image_repo ?? "",
    image_tag: raw.image_tag ?? "",
    image_digest: raw.image_digest ?? "",
    scanner: raw.scanner ?? "",
    scanner_version: raw.scanner_version ?? "",
    critical_count: raw.critical_count ?? 0,
    high_count: raw.high_count ?? 0,
    medium_count: raw.medium_count ?? 0,
    low_count: raw.low_count ?? 0,
    unknown_count: raw.unknown_count ?? 0,
    scanned_at: raw.scanned_at ?? "",
    created_at: raw.created_at ?? "",
    updated_at: raw.updated_at ?? "",
  }));
  return { items, total: Number(wire.count ?? items.length) };
}

export async function getImageVulnReport(
  clusterId: string,
  reportId: string,
  opts: { severity?: CVESeverity; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<ImageVulnReportDetail> {
  const wire = await generated.getClustersByClusterIdVulnerabilitiesReportsById({
    path: { cluster_id: clusterId, id: reportId },
    query: {
      severity: opts.severity,
      limit: opts.limit,
      offset: opts.offset,
    },
    signal,
  });
  const raw = wire.data;
  return {
    report: mapImageVulnReport(raw.report),
    vulnerabilities: raw.vulnerabilities.map((row) => ({
      id: row.id,
      reportId: row.report_id,
      vulnerabilityId: row.vulnerability_id,
      severity: row.severity,
      pkgName: row.pkg_name,
      installedVersion: row.installed_version,
      fixedVersion: row.fixed_version,
      primaryLink: row.primary_link,
      cvssScore: row.cvss_score,
      title: row.title,
      description: row.description,
    })),
    vulnerabilityTotal: raw.vulnerability_total,
    severityFilter: raw.severity_filter,
    limit: raw.limit,
    offset: raw.offset,
  };
}

export async function triggerImageVulnRescan(
  clusterId: string,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<ImageVulnRescanResult> {
  const response = await generated.postClustersByClusterIdVulnerabilitiesRescan(
    {
      path: { cluster_id: clusterId },
      headerParams: { "Idempotency-Key": options.idempotencyKey },
      signal: options.signal,
    },
  );
  return {
    id: response.data.operation_id,
    status: response.data.status,
    clusterId: response.data.cluster_id,
    requestedAt: response.data.requested_at,
    operationUrl: response.data.operation_url,
  };
}

export async function getImageVulnRescanOperation(
  id: string,
  signal?: AbortSignal,
): Promise<ImageVulnRescanResult> {
  const response = await generated.getWorkloadsOperationsById({
    path: { id },
    signal,
  });
  const operation = response.data;
  if (!operation?.id || !operation.status) {
    throw new Error("Vulnerability rescan operation receipt is incomplete");
  }
  return {
    id: operation.id,
    status: operation.status,
    errorMessage: operation.errorMessage,
    clusterId: "",
    requestedAt: operation.createdAt ?? "",
    operationUrl: `/api/v1/workloads/operations/${operation.id}/`,
  };
}
// CRD-mirror v2 (sprint 069) — "what's installed" read-only views
// ============================================================
//
// Backed by the mirrored_* tables; the per-cluster agent streams
// observe events into Postgres so these reads never round-trip
// through kubectl. The is_default / is_managed / accepted_status
// fields are pre-resolved server-side so the UI doesn't have to
// re-parse annotations or condition arrays per render.

export type MirroredIngressClass = Omit<
  OpenAPIComponents["schemas"]["MirroredIngressClass"],
  "is_default" | "last_seen_at" | "created_at" | "updated_at"
> & {
  isDefault: boolean;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredGatewayClass = Omit<
  OpenAPIComponents["schemas"]["MirroredGatewayClass"],
  | "controller_name"
  | "accepted_status"
  | "last_seen_at"
  | "created_at"
  | "updated_at"
> & {
  controllerName: string;
  // "True" | "False" | "Unknown" | "" (when the Accepted condition is unset).
  acceptedStatus: string;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredNetworkPolicy = Omit<
  OpenAPIComponents["schemas"]["MirroredNetworkPolicy"],
  | "pod_selector"
  | "policy_types"
  | "ingress_rules"
  | "egress_rules"
  | "is_managed"
  | "last_seen_at"
  | "created_at"
  | "updated_at"
> & {
  podSelector: unknown;
  policyTypes: string[];
  ingressRules: unknown[];
  egressRules: unknown[];
  // True when app.kubernetes.io/managed-by=astronomer on the policy's
  // labels at ingest time. The UI surfaces this as a "managed by
  // astronomer" badge so operators can tell at a glance which
  // policies are owned by sprint-068's NetworkPolicy writer.
  isManaged: boolean;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredResourceQuota = Omit<
  OpenAPIComponents["schemas"]["MirroredResourceQuota"],
  "hard" | "used" | "scopes" | "last_seen_at" | "created_at" | "updated_at"
> & {
  // Free-form maps so future-proofed for whatever quota keys
  // upstream Kubernetes carries. Typed as `unknown` so the dashboard
  // can render any shape (`cpu`, `requests.memory`,
  // `count/configmaps`, …) without a per-key DTO bump.
  hard: Record<string, string> | null;
  used: Record<string, string> | null;
  scopes: string[];
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredLimitRange = Omit<
  OpenAPIComponents["schemas"]["MirroredLimitRange"],
  "limits" | "last_seen_at" | "created_at" | "updated_at"
> & {
  limits: unknown[];
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

type IngressClassWire = OpenAPIComponents["schemas"]["MirroredIngressClass"];
type GatewayClassWire = OpenAPIComponents["schemas"]["MirroredGatewayClass"];
type NetworkPolicyWire = OpenAPIComponents["schemas"]["MirroredNetworkPolicy"];
type ResourceQuotaWire = OpenAPIComponents["schemas"]["MirroredResourceQuota"];
type LimitRangeWire = OpenAPIComponents["schemas"]["MirroredLimitRange"];

const mapIngressClass = (wire: IngressClassWire): MirroredIngressClass => ({
  name: wire.name,
  controller: wire.controller,
  parameters: wire.parameters,
  labels: wire.labels,
  annotations: wire.annotations,
  isDefault: wire.is_default,
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapGatewayClass = (wire: GatewayClassWire): MirroredGatewayClass => ({
  name: wire.name,
  description: wire.description,
  parameters: wire.parameters,
  labels: wire.labels,
  annotations: wire.annotations,
  controllerName: wire.controller_name,
  acceptedStatus: wire.accepted_status,
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapNetworkPolicy = (wire: NetworkPolicyWire): MirroredNetworkPolicy => ({
  namespace: wire.namespace,
  name: wire.name,
  labels: wire.labels,
  annotations: wire.annotations,
  podSelector: wire.pod_selector,
  policyTypes: Array.isArray(wire.policy_types) ? (wire.policy_types as string[]) : [],
  ingressRules: Array.isArray(wire.ingress_rules) ? wire.ingress_rules : [],
  egressRules: Array.isArray(wire.egress_rules) ? wire.egress_rules : [],
  isManaged: wire.is_managed,
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapResourceQuota = (wire: ResourceQuotaWire): MirroredResourceQuota => ({
  namespace: wire.namespace,
  name: wire.name,
  labels: wire.labels,
  annotations: wire.annotations,
  hard: wire.hard && typeof wire.hard === "object" ? wire.hard as Record<string, string> : null,
  used: wire.used && typeof wire.used === "object" ? wire.used as Record<string, string> : null,
  scopes: Array.isArray(wire.scopes) ? wire.scopes as string[] : [],
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapLimitRange = (wire: LimitRangeWire): MirroredLimitRange => ({
  namespace: wire.namespace,
  name: wire.name,
  labels: wire.labels,
  annotations: wire.annotations,
  limits: Array.isArray(wire.limits) ? wire.limits : [],
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

export async function listMirroredIngressClasses(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MirroredIngressClass[]> {
  const wire = await generated.getClustersByClusterIdIngressClasses({
    path: { cluster_id: clusterId }, signal,
  });
  return wire.data.map(mapIngressClass);
}

export async function listMirroredGatewayClasses(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MirroredGatewayClass[]> {
  const wire = await generated.getClustersByClusterIdGatewayClasses({
    path: { cluster_id: clusterId }, signal,
  });
  return wire.data.map(mapGatewayClass);
}

export async function listMirroredNetworkPolicies(
  clusterId: string,
  namespace?: string,
  signal?: AbortSignal,
): Promise<MirroredNetworkPolicy[]> {
  const wire = await generated.getClustersByClusterIdNetworkPolicies({
    path: { cluster_id: clusterId }, query: { namespace }, signal,
  });
  return wire.data.map(mapNetworkPolicy);
}

export async function listMirroredResourceQuotas(
  clusterId: string,
  namespace?: string,
  signal?: AbortSignal,
): Promise<MirroredResourceQuota[]> {
  const wire = await generated.getClustersByClusterIdResourceQuotas({
    path: { cluster_id: clusterId }, query: { namespace }, signal,
  });
  return wire.data.map(mapResourceQuota);
}

export async function listMirroredLimitRanges(
  clusterId: string,
  namespace?: string,
  signal?: AbortSignal,
): Promise<MirroredLimitRange[]> {
  const wire = await generated.getClustersByClusterIdLimitRanges({
    path: { cluster_id: clusterId }, query: { namespace }, signal,
  });
  return wire.data.map(mapLimitRange);
}

// ============================================================
// Apiserver allow-list (migration 070)
// ============================================================

export type ApiserverAllowlistMode = "monitor" | "enforce" | "disabled";
export type ApiserverAllowlistSyncStatus =
  "synced" | "drifting" | "pending" | "failed";
export type ApiserverAllowlistProvider =
  "eks" | "gke" | "aks" | "doks" | "self_managed" | "unknown";

export interface ApiserverAllowlistResponse {
  clusterId: string;
  operatorCidrs: string[];
  astronomerEgress: string[];
  emergency: string[];
  desired: string[];
  effective: string[];
  mode: ApiserverAllowlistMode;
  detectedProvider: ApiserverAllowlistProvider;
  syncStatus: ApiserverAllowlistSyncStatus;
  lastError?: string;
  lastReconciledAt?: string;
  drift: boolean;
}

export interface ApiserverAllowlistUpdateRequest {
  cidrs: string[];
  mode: ApiserverAllowlistMode;
  forceApply?: boolean;
}

export interface ApiserverAllowlistSnapshot {
  id: number;
  clusterId: string;
  capturedAt: string;
  effectiveCidrs: string[];
  desiredCidrs: string[];
  drift: boolean;
}

function mapAllowlist(raw: Record<string, unknown>): ApiserverAllowlistResponse {
  return {
    clusterId: String(raw.cluster_id ?? ""),
    operatorCidrs: Array.isArray(raw.operator_cidrs) ? raw.operator_cidrs as string[] : [],
    astronomerEgress: Array.isArray(raw.astronomer_egress) ? raw.astronomer_egress as string[] : [],
    emergency: Array.isArray(raw.emergency) ? raw.emergency as string[] : [],
    desired: Array.isArray(raw.desired) ? raw.desired as string[] : [],
    effective: Array.isArray(raw.effective) ? raw.effective as string[] : [],
    mode: String(raw.mode ?? "disabled") as ApiserverAllowlistMode,
    detectedProvider: String(raw.detected_provider ?? "unknown") as ApiserverAllowlistProvider,
    syncStatus: String(raw.sync_status ?? "pending") as ApiserverAllowlistSyncStatus,
    lastError: raw.last_error ? String(raw.last_error) : undefined,
    lastReconciledAt: raw.last_reconciled_at ? String(raw.last_reconciled_at) : undefined,
    drift: Boolean(raw.drift),
  };
}

function mapAllowlistSnapshot(raw: Record<string, unknown>): ApiserverAllowlistSnapshot {
  return {
    id: Number(raw.id ?? 0),
    clusterId: String(raw.cluster_id ?? ""),
    capturedAt: String(raw.captured_at ?? ""),
    effectiveCidrs: Array.isArray(raw.effective_cidrs) ? raw.effective_cidrs as string[] : [],
    desiredCidrs: Array.isArray(raw.desired_cidrs) ? raw.desired_cidrs as string[] : [],
    drift: Boolean(raw.drift),
  };
}

export async function getApiserverAllowlist(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ApiserverAllowlistResponse> {
  const wire = await generated.getClustersByClusterIdApiserverAllowlist({
    path: { cluster_id: clusterId }, signal,
  });
  return mapAllowlist(requiredClusterDetailData(wire, "API server allowlist"));
}

export async function previewApiserverAllowlist(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ApiserverAllowlistResponse> {
  const wire = await generated.getClustersByClusterIdApiserverAllowlistPreview({
    path: { cluster_id: clusterId }, signal,
  });
  return mapAllowlist(requiredClusterDetailData(wire, "API server allowlist preview"));
}

export async function updateApiserverAllowlist(
  clusterId: string,
  body: ApiserverAllowlistUpdateRequest,
  signal?: AbortSignal,
): Promise<ApiserverAllowlistResponse> {
  const wire = await generated.putClustersByClusterIdApiserverAllowlist({
    path: { cluster_id: clusterId },
    body: { cidrs: body.cidrs, mode: body.mode, force_apply: body.forceApply },
    signal,
  });
  return mapAllowlist(requiredClusterDetailData(wire, "API server allowlist update"));
}

export async function reconcileApiserverAllowlist(
  clusterId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.postClustersByClusterIdApiserverAllowlistReconcile({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    signal,
  });
}

export async function listApiserverAllowlistSnapshots(
  clusterId: string,
  opts?: { limit?: number; offset?: number },
  signal?: AbortSignal,
): Promise<ApiserverAllowlistSnapshot[]> {
  const wire = await generated.getClustersByClusterIdApiserverAllowlistSnapshots({
    path: { cluster_id: clusterId }, query: opts, signal,
  });
  return (wire.data?.items ?? []).map(mapAllowlistSnapshot);
}
// Service Mesh tile (migration 071)
//
// Three endpoints — current detection, on-demand re-detect, mTLS breakdown.
// All gated on clusters:read on the backend; the UI wraps them in the
// same auth context as the rest of the cluster-detail surface.
// ============================================================

export type ServiceMeshKind =
  "istio" | "linkerd" | "kuma" | "cilium" | "none" | "unknown";

export interface ServiceMeshDetection {
  clusterId: string;
  detectedMesh: ServiceMeshKind;
  detectedVersion: string;
  controlPlaneNamespace: string;
  gatewayCount: number;
  virtualServiceCount: number;
  destinationRuleCount: number;
  peerAuthenticationCount: number;
  serviceProfileCount: number;
  serverAuthCount: number;
  mtlsCoveragePct: number;
  lastDetectedAt?: string;
  lastError?: string;
}

export interface MTLSBreakdownRow {
  namespace: string;
  mode: string;
  rules: number;
}

export interface MTLSBreakdown {
  clusterId: string;
  mesh: ServiceMeshKind;
  mtlsCoveragePct: number;
  totalCount: number;
  rows: MTLSBreakdownRow[];
  notice?: string;
}

export interface ServiceMeshInventoryItem {
  name: string;
  namespace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  managedBy?: string;
  readOnly: boolean;
  reason?: string;
  createdAt?: string;
}

export interface ServiceMeshInventoryResource {
  kind: string;
  apiVersion: string;
  plural: string;
  count: number;
  items: ServiceMeshInventoryItem[];
  notice?: string;
}

export interface ServiceMeshInventory {
  clusterId: string;
  mesh: ServiceMeshKind;
  resources: ServiceMeshInventoryResource[];
  totalCount: number;
  notice?: string;
}

export interface ServiceMeshPolicyValidationFinding {
  field?: string;
  severity: "warning" | "error" | string;
  message: string;
}

export interface ServiceMeshPolicyValidation {
  clusterId: string;
  valid: boolean;
  apiVersion?: string;
  kind?: string;
  name?: string;
  namespace?: string;
  managedBy?: string;
  readOnly: boolean;
  applyAllowed: boolean;
  warnings: ServiceMeshPolicyValidationFinding[];
  errors: ServiceMeshPolicyValidationFinding[];
}

function mapServiceMeshDetection(raw: Record<string, unknown>): ServiceMeshDetection {
  return {
    clusterId: String(raw.cluster_id ?? ""),
    detectedMesh: String(raw.detected_mesh ?? "unknown") as ServiceMeshKind,
    detectedVersion: String(raw.detected_version ?? ""),
    controlPlaneNamespace: String(raw.control_plane_namespace ?? ""),
    gatewayCount: Number(raw.gateway_count ?? 0),
    virtualServiceCount: Number(raw.virtual_service_count ?? 0),
    destinationRuleCount: Number(raw.destination_rule_count ?? 0),
    peerAuthenticationCount: Number(raw.peer_authentication_count ?? 0),
    serviceProfileCount: Number(raw.service_profile_count ?? 0),
    serverAuthCount: Number(raw.server_auth_count ?? 0),
    mtlsCoveragePct: Number(raw.mtls_coverage_pct ?? 0),
    lastDetectedAt: raw.last_detected_at ? String(raw.last_detected_at) : undefined,
    lastError: raw.last_error ? String(raw.last_error) : undefined,
  };
}

function mapServiceMeshInventory(
  raw: OpenAPIComponents["schemas"]["ServiceMeshInventory"],
): ServiceMeshInventory {
  return {
    clusterId: raw.cluster_id ?? "",
    mesh: (raw.mesh ?? "unknown") as ServiceMeshKind,
    totalCount: raw.total_count ?? 0,
    notice: raw.notice,
    resources: (raw.resources ?? []).map((resource) => ({
      kind: resource.kind ?? "",
      apiVersion: resource.api_version ?? "",
      plural: resource.plural ?? "",
      count: resource.count ?? 0,
      notice: resource.notice,
      items: (resource.items ?? []).map((item) => ({
        name: item.name ?? "",
        namespace: item.namespace,
        managedBy: item.managed_by,
        readOnly: item.read_only ?? false,
        reason: item.reason,
      })),
    })),
  };
}

function mapValidationFinding(raw: Record<string, unknown>): ServiceMeshPolicyValidationFinding {
  return {
    field: raw.field ? String(raw.field) : undefined,
    severity: String(raw.severity ?? "warning"),
    message: String(raw.message ?? ""),
  };
}

export async function getServiceMeshDetection(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ServiceMeshDetection> {
  const wire = await generated.getClustersByClusterIdServiceMesh({
    path: { cluster_id: clusterId }, signal,
  });
  return mapServiceMeshDetection(requiredClusterDetailData(wire, "Service mesh detection"));
}

export async function reDetectServiceMesh(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ServiceMeshDetection> {
  const wire = await generated.postClustersByClusterIdServiceMeshDetect({
    path: { cluster_id: clusterId }, signal,
  });
  return mapServiceMeshDetection(requiredClusterDetailData(wire, "Service mesh detection"));
}

export async function getServiceMeshMTLS(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MTLSBreakdown> {
  const wire = await generated.getClustersByClusterIdServiceMeshMtls({
    path: { cluster_id: clusterId }, signal,
  });
  const raw = requiredClusterDetailData(wire, "Service mesh mTLS") as Record<string, unknown>;
  return {
    clusterId: String(raw.cluster_id ?? ""),
    mesh: String(raw.mesh ?? "unknown") as ServiceMeshKind,
    mtlsCoveragePct: Number(raw.mtls_coverage_pct ?? 0),
    totalCount: Number(raw.total_count ?? 0),
    notice: raw.notice ? String(raw.notice) : undefined,
    rows: (Array.isArray(raw.rows) ? raw.rows : []).map((item) => {
      const row = item as Record<string, unknown>;
      return { namespace: String(row.namespace ?? ""), mode: String(row.mode ?? ""), rules: Number(row.rules ?? 0) };
    }),
  };
}

export async function getServiceMeshInventory(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ServiceMeshInventory> {
  const wire = await generated.getClustersByClusterIdServiceMeshInventory({
    path: { cluster_id: clusterId }, signal,
  });
  return mapServiceMeshInventory(requiredClusterDetailData(wire, "Service mesh inventory"));
}

export async function validateServiceMeshPolicy(
  clusterId: string,
  body: { yaml?: string; object?: unknown },
  signal?: AbortSignal,
): Promise<ServiceMeshPolicyValidation> {
  const requestBody = body.yaml !== undefined
    ? { yaml: body.yaml }
    : { object: body.object as Record<string, unknown> | undefined };
  const wire = await generated.postClustersByClusterIdServiceMeshValidate({
    path: { cluster_id: clusterId }, body: requestBody, signal,
  });
  const raw = requiredClusterDetailData(wire, "Service mesh policy validation");
  return {
    clusterId: raw.cluster_id ?? "",
    valid: raw.valid ?? false,
    apiVersion: raw.api_version,
    kind: raw.kind,
    name: raw.name,
    namespace: raw.namespace,
    managedBy: raw.managed_by,
    readOnly: raw.read_only ?? false,
    applyAllowed: raw.apply_allowed ?? false,
    warnings: (raw.warnings ?? []).map(mapValidationFinding),
    errors: (raw.errors ?? []).map(mapValidationFinding),
  };
}

// ---------------------------------------------------------------------
// Sprint 081 — scan history + diff + CSV.
// ---------------------------------------------------------------------

export interface ImageVulnHistoryPoint {
  scannedAt: string;
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
  reportCount: number;
}

export interface ImageVulnHistoryResponse {
  clusterId: string;
  since: string;
  snapshots: ImageVulnHistoryPoint[];
  totalCount: number;
}

export async function getImageVulnHistory(
  clusterId: string,
  opts: { sinceHours?: number; limit?: number } = {},
  signal?: AbortSignal,
): Promise<ImageVulnHistoryResponse> {
  const wire = await generated.getClustersByClusterIdVulnerabilitiesHistory({
    path: { cluster_id: clusterId },
    query: { since_hours: opts.sinceHours, limit: opts.limit },
    signal,
  });
  const raw = wire.data;
  const snapshots = raw.snapshots.map((s) => ({
    scannedAt: s.scanned_at,
    critical: s.critical,
    high: s.high,
    medium: s.medium,
    low: s.low,
    unknown: s.unknown,
    reportCount: s.report_count,
  }));
  return {
    clusterId: raw.cluster_id,
    since: raw.since,
    snapshots,
    totalCount: raw.total_count,
  };
}

export interface ImageVulnDiffBucket {
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
  scannedAt: string;
}

export interface ImageVulnDiff {
  clusterId: string;
  hasComparison: boolean;
  priorHours: number;
  latest?: ImageVulnDiffBucket;
  prior?: ImageVulnDiffBucket;
  delta?: {
    critical: number;
    high: number;
    medium: number;
    low: number;
    unknown: number;
  };
}

export async function getImageVulnDiff(
  clusterId: string,
  priorHours = 24,
  signal?: AbortSignal,
): Promise<ImageVulnDiff> {
  const wire = await generated.getClustersByClusterIdVulnerabilitiesDiff({
    path: { cluster_id: clusterId }, query: { prior_hours: priorHours }, signal,
  });
  const raw = wire.data;
  const bucket = (b: OpenAPIComponents["schemas"]["VulnerabilityDiffBucket"] | undefined): ImageVulnDiffBucket | undefined => {
    if (!b) return undefined;
    return {
      critical: b.critical,
      high: b.high,
      medium: b.medium,
      low: b.low,
      unknown: b.unknown,
      scannedAt: b.scanned_at,
    };
  };
  return {
    clusterId: raw.cluster_id,
    hasComparison: raw.has_comparison,
    priorHours: raw.prior_hours,
    latest: bucket(raw.latest),
    prior: bucket(raw.prior),
    delta: raw.delta,
  };
}

export function exportImageVulnsCSVPath(clusterId: string): string {
  return `/api/v1/clusters/${clusterId}/vulnerabilities/export.csv`;
}

// Per-image scan history — powers the drawer's "scan history" panel.
// Lighter shape than ImageVulnHistoryPoint (no reportCount/since).
export interface ImageVulnReportHistoryPoint {
  scannedAt: string;
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
}

export interface ImageVulnReportHistoryResponse {
  reportId: string;
  snapshots: ImageVulnReportHistoryPoint[];
  totalCount: number;
}

export async function getImageVulnReportHistory(
  clusterId: string,
  reportId: string,
  opts: { limit?: number } = {},
  signal?: AbortSignal,
): Promise<ImageVulnReportHistoryResponse> {
  const wire = await generated.getClustersByClusterIdVulnerabilitiesReportsByReportIdHistory({
    path: { cluster_id: clusterId, report_id: reportId },
    query: { limit: opts.limit }, signal,
  });
  const raw = wire.data;
  const snapshots = raw.snapshots.map((s) => ({
    scannedAt: s.scanned_at,
    critical: s.critical,
    high: s.high,
    medium: s.medium,
    low: s.low,
    unknown: s.unknown,
  }));
  return {
    reportId: raw.report_id,
    snapshots,
    totalCount: raw.total_count,
  };
}

// ---------------------------------------------------------------------
// Scan-in-progress indicator (sprint 081).
// ---------------------------------------------------------------------

export interface ImageVulnProgress {
  scanning: boolean;
  activeJobs: number;
  completedJobs: number;
  failedJobs: number;
  reportsCount: number;
  trivyOperatorReady: boolean;
  lastScanAgeSeconds: number | null;
}

export async function getImageVulnProgress(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ImageVulnProgress> {
  const wire = await generated.getClustersByClusterIdVulnerabilitiesProgress({
    path: { cluster_id: clusterId }, signal,
  });
  const raw = wire.data;
  // Snake → camel mapping. If we leave the snake names exposed to the
  // page (`progress.trivy_operator_ready`) a typo'd accessor on the
  // camelCase form silently reads undefined and renders the wrong
  // banner state — which is exactly the "trivy not ready / 0 scans /
  // invalid date" bug operators were seeing.
  return {
    scanning: raw.scanning,
    activeJobs: raw.active_jobs,
    completedJobs: raw.completed_jobs,
    failedJobs: raw.failed_jobs,
    reportsCount: raw.reports_count,
    trivyOperatorReady: raw.trivy_operator_ready,
    lastScanAgeSeconds: raw.last_scan_age_seconds,
  };
}

// ---------------------------------------------------------------------
// Sprint 082+ — per-cluster Apps tab.
//
// Wraps the enriched /clusters/{id}/apps/ endpoint plus the existing
// /catalog/* surfaces (browse charts, get chart values, install).
// Snake→camel mapping at the boundary so page components can stay
// camelCase end-to-end.
// ---------------------------------------------------------------------

export interface ClusterAppRow {
  id: string;
  clusterId: string;
  // chartId is the parent helm_charts UUID; needed by the Upgrade
  // modal to drive the version dropdown. Empty for Tools installs
  // (chart_version_id NULL → JOIN collapses to NULL).
  chartId: string;
  chartVersionId: string;
  releaseName: string;
  namespace: string;
  status: string;
  revision: number;
  // valuesOverride is the user's current helm values_override on
  // the release — pre-fills the Upgrade modal's YAML editor.
  valuesOverride: string;
  toolSlug: string;
  presetUsed: string;
  // sourceKind = 'app' for catalog-installed releases, 'tool' for
  // Platform Baseline / Tools-tab installs. Drives the "Managed by
  // Tools" pivot pill in the UI.
  sourceKind: "app" | "tool";
  displayName: string;
  chartName: string;
  chartVersion: string;
  chartAppVersion: string;
  chartDescription: string;
  chartIconUrl: string;
  chartCategory: string;
  repoName: string;
  repoType: string;
  createdAt: string;
  updatedAt: string;
}

export interface ClusterAppsResponse {
  items: ClusterAppRow[];
  total: number;
}

export async function listClusterApps(
  clusterId: string,
  opts: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<ClusterAppsResponse> {
  const wire = await generated.getClustersByClusterIdApps({
    path: { cluster_id: clusterId }, query: opts, signal,
  });
  const rows = (wire.data ?? []) as OpenAPIComponents["schemas"]["InstalledAppEnriched"][];
  const items: ClusterAppRow[] = rows.map((raw) => ({
    id: raw.id ?? "",
    clusterId: raw.cluster_id ?? "",
    chartId: raw.chart_id ?? "",
    chartVersionId: raw.chart_version_id ?? "",
    releaseName: raw.release_name ?? "",
    namespace: raw.namespace ?? "",
    status: raw.status ?? "",
    revision: raw.revision ?? 0,
    valuesOverride: raw.values_override ?? "",
    toolSlug: raw.tool_slug ?? "",
    presetUsed: raw.preset_used ?? "",
    sourceKind: raw.source_kind ?? "app",
    displayName: raw.display_name ?? "",
    chartName: raw.chart_name ?? "",
    chartVersion: raw.chart_version ?? "",
    chartAppVersion: raw.chart_app_version ?? "",
    chartDescription: raw.chart_description ?? "",
    chartIconUrl: raw.chart_icon_url ?? "",
    chartCategory: raw.chart_category ?? "",
    repoName: raw.repo_name ?? "",
    repoType: raw.repo_type ?? "",
    createdAt: raw.created_at ?? "",
    updatedAt: raw.updated_at ?? "",
  }));
  return { items, total: wire.count ?? items.length };
}

// Browse view: lists charts in the catalog. Wraps existing
// /catalog/charts/ but normalises the response shape and snake→camel.
export interface CatalogChartSummary {
  id: string;
  repositoryId: string;
  name: string;
  displayName: string;
  description: string;
  iconUrl: string;
  homeUrl: string;
  category: string;
  keywords: string[];
  deprecated: boolean;
}

export async function listCatalogCharts(params: {
  projectId: string;
  limit?: number;
  offset?: number;
  search?: string;
  signal?: AbortSignal;
}): Promise<{ items: CatalogChartSummary[]; total: number }> {
  const wire = await generated.getCatalogCharts({
    query: {
      project_id: params.projectId,
      limit: params.limit,
      offset: params.offset,
    },
    signal: params.signal,
  });
  const rows = (wire.data ?? []) as OpenAPIComponents["schemas"]["HelmChart"][];
  const items: CatalogChartSummary[] = rows.map((raw) => ({
    id: raw.id ?? "",
    repositoryId: raw.repository_id ?? "",
    name: raw.name ?? "",
    displayName: raw.display_name ?? raw.name ?? "",
    description: raw.description ?? "",
    iconUrl: raw.icon_url ?? "",
    homeUrl: raw.home_url ?? "",
    category: raw.category ?? "",
    keywords: raw.keywords ?? [],
    deprecated: raw.deprecated ?? false,
  }));
  return { items, total: wire.count ?? items.length };
}

// Recommended view: wraps /catalog/recommendations/popular which
// returns top charts by install + rating score.
export interface RecommendedChart {
  chartId: string;
  name: string;
  score: number;
  ratingAvg: number;
  installCount: number;
}

export async function listRecommendedCharts(
  projectId: string,
  limit = 10,
  signal?: AbortSignal,
): Promise<RecommendedChart[]> {
  const wire = await generated.getCatalogRecommendationsPopular({
    query: { project_id: projectId, limit }, signal,
  });
  const rows = (wire.data ?? []) as OpenAPIComponents["schemas"]["ChartRecommendation"][];
  return rows.map((raw) => ({
    chartId: raw.chart_id,
    name: "",
    score: raw.bayesian_score,
    ratingAvg: raw.avg_stars,
    installCount: raw.rating_count,
  }));
}

// Chart-version list for the install modal's version dropdown.
export interface ChartVersionRow {
  id: string;
  version: string;
  appVersion: string;
  createdAtUpstream: string;
}

export async function listChartVersions(
  projectId: string,
  chartId: string,
  signal?: AbortSignal,
): Promise<ChartVersionRow[]> {
  const wire = await generated.getCatalogChartsByIdVersions({
    path: { id: chartId }, query: { project_id: projectId, limit: 50 }, signal,
  });
  return wire.data.map((raw) => ({
    id: raw.id ?? "",
    version: raw.version ?? "",
    appVersion: raw.app_version ?? "",
    createdAtUpstream: raw.created_at_upstream ?? "",
  }));
}

// Default values.yaml for the install modal's YAML editor pre-fill.
// First call on a given version triggers backend hydration (~1-2s);
// subsequent calls are cached in the DB row.
export async function getChartDefaultValues(
  projectId: string,
  chartId: string,
  version?: string,
  signal?: AbortSignal,
): Promise<{ chart: string; version: string; defaultValues: string }> {
  const wire = await generated.getCatalogChartsByIdValues({
    path: { id: chartId },
    query: { project_id: projectId, version },
    signal,
  });
  return {
    chart: wire.chart ?? "",
    version: wire.version ?? "",
    defaultValues: wire.default_values ?? "",
  };
}

// Kick off a fresh install on this cluster. Returns the created
// installed_charts row id; the helm install itself happens
// asynchronously via the tunnel + worker queue.
export async function installChartOnCluster(req: {
  projectId: string;
  clusterId: string;
  chartVersionId: string;
  releaseName: string;
  namespace: string;
  valuesOverride: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
}): Promise<{ id: string }> {
  const wire = await generated.postCatalogInstalled({
    headerParams: { "Idempotency-Key": req.idempotencyKey ?? createIdempotencyKey() },
    body: {
      project_id: req.projectId,
      cluster_id: req.clusterId,
      chart_version_id: req.chartVersionId,
      release_name: req.releaseName,
      namespace: req.namespace,
      values_override: req.valuesOverride,
    },
    signal: req.signal,
  });
  return { id: wire.data.installation.id ?? "" };
}

export async function uninstallCatalogRelease(
  installedChartId: string,
  options: { idempotencyKey?: string; signal?: AbortSignal } = {},
): Promise<void> {
  await generated.deleteCatalogInstalledById({
    path: { id: installedChartId },
    headerParams: { "Idempotency-Key": options.idempotencyKey ?? createIdempotencyKey() },
    signal: options.signal,
  });
}

// Rancher-style bulk-delete of stuck releases. Backend hard-deletes any
// installed_charts rows in failed_install / failed_uninstall on this
// cluster and returns the affected row count.
export async function deleteFailedClusterApps(
  clusterId: string,
  signal?: AbortSignal,
): Promise<{ deleted: number }> {
  const wire = await generated.deleteClustersByClusterIdAppsFailed({
    path: { cluster_id: clusterId }, signal,
  });
  return { deleted: wire.deleted ?? 0 };
}
