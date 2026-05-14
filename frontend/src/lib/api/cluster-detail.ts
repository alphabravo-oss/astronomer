/**
 * Cluster-detail API client — Velero snapshots/schedules, private registries,
 * and cluster-template binding. Backend endpoints live under
 * `/api/v1/clusters/{cluster_id}/…` and are camelized by the shared axios
 * interceptor in ../api.ts, so all response types use camelCase keys.
 *
 * Re-exported from ../api.ts via `export * from './api/cluster-detail'`.
 */

import api from '../api';
import type { APIResponse } from '@/types';

// ============================================================
// Snapshots (Velero)
// ============================================================

export type SnapshotPhase =
  | 'New'
  | 'InProgress'
  | 'Completed'
  | 'PartiallyFailed'
  | 'Failed'
  | 'FailedValidation'
  | 'Deleting'
  | string;

export interface SnapshotSpec {
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  includedResources?: string[];
  excludedResources?: string[];
  snapshotVolumes?: boolean;
  ttl?: string;
  storageLocation?: string;
  labelSelector?: Record<string, string>;
}

export interface Snapshot {
  id: string;
  name: string;
  source: 'adhoc' | 'schedule';
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

export interface VeleroStatus {
  installed: boolean;
  bslReady: boolean;
  vslReady?: boolean;
  version?: string;
  storageLocation?: string;
  message?: string;
}

export async function getVeleroStatus(clusterId: string): Promise<VeleroStatus> {
  const res = await api.get<APIResponse<VeleroStatus>>(`/clusters/${clusterId}/velero-status`);
  return res.data.data;
}

export async function listSnapshots(clusterId: string): Promise<Snapshot[]> {
  const res = await api.get<APIResponse<Snapshot[]>>(`/clusters/${clusterId}/snapshots`);
  return res.data.data ?? [];
}

export async function createSnapshot(
  clusterId: string,
  body: { spec: SnapshotSpec },
): Promise<Snapshot> {
  const res = await api.post<APIResponse<Snapshot>>(`/clusters/${clusterId}/snapshots`, body);
  return res.data.data;
}

export async function deleteSnapshot(clusterId: string, snapshotId: string): Promise<void> {
  await api.delete(`/clusters/${clusterId}/snapshots/${snapshotId}`);
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
): Promise<SnapshotRestore> {
  const res = await api.post<APIResponse<SnapshotRestore>>(
    `/clusters/${clusterId}/snapshots/${snapshotId}/restore`,
    body,
  );
  return res.data.data;
}

export async function listSnapshotSchedules(clusterId: string): Promise<SnapshotSchedule[]> {
  const res = await api.get<APIResponse<SnapshotSchedule[]>>(
    `/clusters/${clusterId}/snapshot-schedules`,
  );
  return res.data.data ?? [];
}

export async function createSnapshotSchedule(
  clusterId: string,
  body: { name: string; cron: string; enabled?: boolean; spec: SnapshotSpec },
): Promise<SnapshotSchedule> {
  const res = await api.post<APIResponse<SnapshotSchedule>>(
    `/clusters/${clusterId}/snapshot-schedules`,
    body,
  );
  return res.data.data;
}

export async function updateSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
  body: Partial<{ name: string; cron: string; enabled: boolean; spec: SnapshotSpec }>,
): Promise<SnapshotSchedule> {
  const res = await api.put<APIResponse<SnapshotSchedule>>(
    `/clusters/${clusterId}/snapshot-schedules/${scheduleId}`,
    body,
  );
  return res.data.data;
}

export async function deleteSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
): Promise<void> {
  await api.delete(`/clusters/${clusterId}/snapshot-schedules/${scheduleId}`);
}

// ============================================================
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
  message?: string;
  latencyMs?: number;
}

export async function listClusterRegistries(clusterId: string): Promise<ClusterRegistry[]> {
  const res = await api.get<APIResponse<ClusterRegistry[]>>(
    `/clusters/${clusterId}/registries`,
  );
  return res.data.data ?? [];
}

export async function createClusterRegistry(
  clusterId: string,
  body: CreateRegistryRequest,
): Promise<ClusterRegistry> {
  const res = await api.post<APIResponse<ClusterRegistry>>(
    `/clusters/${clusterId}/registries`,
    body,
  );
  return res.data.data;
}

export async function updateClusterRegistry(
  clusterId: string,
  registryId: string,
  body: UpdateRegistryRequest,
): Promise<ClusterRegistry> {
  const res = await api.put<APIResponse<ClusterRegistry>>(
    `/clusters/${clusterId}/registries/${registryId}`,
    body,
  );
  return res.data.data;
}

export async function deleteClusterRegistry(
  clusterId: string,
  registryId: string,
): Promise<void> {
  await api.delete(`/clusters/${clusterId}/registries/${registryId}`);
}

export async function testClusterRegistry(
  clusterId: string,
  registryId: string,
): Promise<RegistryTestResult> {
  const res = await api.post<APIResponse<RegistryTestResult>>(
    `/clusters/${clusterId}/registries/${registryId}/test`,
  );
  return res.data.data;
}

// ============================================================
// Cluster template binding
// ============================================================

export type ClusterTemplateStatus = 'pending' | 'applying' | 'applied' | 'failed' | string;

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

export async function getClusterTemplateBinding(
  clusterId: string,
): Promise<ClusterTemplateBinding | null> {
  try {
    const res = await api.get<APIResponse<ClusterTemplateBinding>>(
      `/clusters/${clusterId}/template`,
    );
    return res.data.data ?? null;
  } catch (err) {
    // 404 — no template bound. Anything else surfaces to the caller.
    const status = (err as { response?: { status?: number } })?.response?.status;
    if (status === 404) return null;
    throw err;
  }
}

export async function bindClusterTemplate(
  clusterId: string,
  body: { template_id: string },
): Promise<ClusterTemplateBinding> {
  const res = await api.post<APIResponse<ClusterTemplateBinding>>(
    `/clusters/${clusterId}/template`,
    body,
  );
  return res.data.data;
}

export async function reapplyClusterTemplate(
  clusterId: string,
): Promise<ClusterTemplateBinding> {
  const res = await api.post<APIResponse<ClusterTemplateBinding>>(
    `/clusters/${clusterId}/template/reapply`,
  );
  return res.data.data;
}

export async function detachClusterTemplate(clusterId: string): Promise<void> {
  await api.delete(`/clusters/${clusterId}/template`);
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

export type CVESeverity = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'UNKNOWN' | string;

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

export interface ImageVulnRescanResult {
  triggered: boolean;
  reason?: string;
  error?: string;
  clusterId: string;
  requestedAt: string;
}

export async function getImageVulnSummary(clusterId: string): Promise<ImageVulnSummary> {
  // The Go backend returns snake_case (report_count, last_scanned_at);
  // the TS interface uses camelCase for everywhere-else convenience.
  // Map at the boundary so a stray `summary.lastScannedAt` doesn't
  // silently read undefined → "Invalid Date" + a "0 reports" tile,
  // which is exactly the bug operators were seeing on this page.
  const res = await api.get<{ data: Record<string, unknown> }>(
    `/clusters/${clusterId}/vulnerabilities/summary/`,
  );
  const raw = res.data.data;
  return {
    critical: Number(raw.critical ?? 0),
    high: Number(raw.high ?? 0),
    medium: Number(raw.medium ?? 0),
    low: Number(raw.low ?? 0),
    unknown: Number(raw.unknown ?? 0),
    reportCount: Number(raw.report_count ?? raw.reportCount ?? 0),
    lastScannedAt: (raw.last_scanned_at as string | null) ?? (raw.lastScannedAt as string | null) ?? null,
  };
}

export async function listVulnerableImages(
  clusterId: string,
  opts: { namespace?: string; limit?: number; offset?: number } = {},
): Promise<{ items: ImageVulnReport[]; total: number }> {
  const q = new URLSearchParams();
  if (opts.namespace) q.set('namespace', opts.namespace);
  if (opts.limit != null) q.set('limit', String(opts.limit));
  if (opts.offset != null) q.set('offset', String(opts.offset));
  const suffix = q.toString() ? `?${q}` : '';
  const res = await api.get<{ data: Record<string, unknown>[]; count: number }>(
    `/clusters/${clusterId}/vulnerabilities/images/${suffix}`,
  );
  // Snake → camel at the boundary. The Go side returns
  // critical_count / scanned_at / image_repo etc.; the TS interface
  // calls them criticalCount / scannedAt / imageRepo. Without this
  // mapping, every property read on a row is undefined → table shows
  // 0 for every count column and "Invalid Date" for scannedAt.
  const items: ImageVulnReport[] = (res.data.data ?? []).map((raw) => ({
    id: String(raw.id ?? ''),
    clusterId: String(raw.cluster_id ?? raw.clusterId ?? ''),
    reportName: String(raw.report_name ?? raw.reportName ?? ''),
    namespace: String(raw.namespace ?? ''),
    workloadKind: String(raw.workload_kind ?? raw.workloadKind ?? ''),
    workloadName: String(raw.workload_name ?? raw.workloadName ?? ''),
    containerName: String(raw.container_name ?? raw.containerName ?? ''),
    imageRegistry: String(raw.image_registry ?? raw.imageRegistry ?? ''),
    imageRepo: String(raw.image_repo ?? raw.imageRepo ?? ''),
    imageTag: String(raw.image_tag ?? raw.imageTag ?? ''),
    imageDigest: String(raw.image_digest ?? raw.imageDigest ?? ''),
    scanner: String(raw.scanner ?? ''),
    scannerVersion: String(raw.scanner_version ?? raw.scannerVersion ?? ''),
    criticalCount: Number(raw.critical_count ?? raw.criticalCount ?? 0),
    highCount: Number(raw.high_count ?? raw.highCount ?? 0),
    mediumCount: Number(raw.medium_count ?? raw.mediumCount ?? 0),
    lowCount: Number(raw.low_count ?? raw.lowCount ?? 0),
    unknownCount: Number(raw.unknown_count ?? raw.unknownCount ?? 0),
    scannedAt: String(raw.scanned_at ?? raw.scannedAt ?? ''),
    createdAt: String(raw.created_at ?? raw.createdAt ?? ''),
    updatedAt: String(raw.updated_at ?? raw.updatedAt ?? ''),
  }));
  return { items, total: Number(res.data.count ?? items.length) };
}

export async function getImageVulnReport(
  clusterId: string,
  reportId: string,
  opts: { severity?: CVESeverity; limit?: number; offset?: number } = {},
): Promise<ImageVulnReportDetail> {
  const q = new URLSearchParams();
  if (opts.severity) q.set('severity', opts.severity);
  if (opts.limit != null) q.set('limit', String(opts.limit));
  if (opts.offset != null) q.set('offset', String(opts.offset));
  const suffix = q.toString() ? `?${q}` : '';
  const res = await api.get<APIResponse<ImageVulnReportDetail>>(
    `/clusters/${clusterId}/vulnerabilities/reports/${reportId}/${suffix}`,
  );
  return res.data.data;
}

export async function triggerImageVulnRescan(
  clusterId: string,
): Promise<ImageVulnRescanResult> {
  const res = await api.post<APIResponse<ImageVulnRescanResult>>(
    `/clusters/${clusterId}/vulnerabilities/rescan/`,
  );
  return res.data.data;
}
// CRD-mirror v2 (sprint 069) — "what's installed" read-only views
// ============================================================
//
// Backed by the mirrored_* tables; the per-cluster agent streams
// observe events into Postgres so these reads never round-trip
// through kubectl. The is_default / is_managed / accepted_status
// fields are pre-resolved server-side so the UI doesn't have to
// re-parse annotations or condition arrays per render.

export interface MirroredIngressClass {
  name: string;
  controller: string;
  parameters: unknown;
  isDefault: boolean;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface MirroredGatewayClass {
  name: string;
  controllerName: string;
  description: string;
  parameters: unknown;
  // "True" | "False" | "Unknown" | "" (when the Accepted condition is unset).
  acceptedStatus: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface MirroredNetworkPolicy {
  namespace: string;
  name: string;
  podSelector: unknown;
  policyTypes: string[];
  ingressRules: unknown[];
  egressRules: unknown[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  // True when app.kubernetes.io/managed-by=astronomer on the policy's
  // labels at ingest time. The UI surfaces this as a "managed by
  // astronomer" badge so operators can tell at a glance which
  // policies are owned by sprint-068's NetworkPolicy writer.
  isManaged: boolean;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface MirroredResourceQuota {
  namespace: string;
  name: string;
  // Free-form maps so future-proofed for whatever quota keys
  // upstream Kubernetes carries. Typed as `unknown` so the dashboard
  // can render any shape (`cpu`, `requests.memory`,
  // `count/configmaps`, …) without a per-key DTO bump.
  hard: Record<string, string> | null;
  used: Record<string, string> | null;
  scopes: string[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface MirroredLimitRange {
  namespace: string;
  name: string;
  limits: unknown[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
}

export async function listMirroredIngressClasses(
  clusterId: string,
): Promise<MirroredIngressClass[]> {
  const res = await api.get<APIResponse<MirroredIngressClass[]>>(
    `/clusters/${clusterId}/ingress-classes`,
  );
  return res.data.data ?? [];
}

export async function listMirroredGatewayClasses(
  clusterId: string,
): Promise<MirroredGatewayClass[]> {
  const res = await api.get<APIResponse<MirroredGatewayClass[]>>(
    `/clusters/${clusterId}/gateway-classes`,
  );
  return res.data.data ?? [];
}

export async function listMirroredNetworkPolicies(
  clusterId: string,
  namespace?: string,
): Promise<MirroredNetworkPolicy[]> {
  const url = namespace
    ? `/clusters/${clusterId}/network-policies?namespace=${encodeURIComponent(namespace)}`
    : `/clusters/${clusterId}/network-policies`;
  const res = await api.get<APIResponse<MirroredNetworkPolicy[]>>(url);
  return res.data.data ?? [];
}

export async function listMirroredResourceQuotas(
  clusterId: string,
  namespace?: string,
): Promise<MirroredResourceQuota[]> {
  const url = namespace
    ? `/clusters/${clusterId}/resource-quotas?namespace=${encodeURIComponent(namespace)}`
    : `/clusters/${clusterId}/resource-quotas`;
  const res = await api.get<APIResponse<MirroredResourceQuota[]>>(url);
  return res.data.data ?? [];
}

export async function listMirroredLimitRanges(
  clusterId: string,
  namespace?: string,
): Promise<MirroredLimitRange[]> {
  const url = namespace
    ? `/clusters/${clusterId}/limit-ranges?namespace=${encodeURIComponent(namespace)}`
    : `/clusters/${clusterId}/limit-ranges`;
  const res = await api.get<APIResponse<MirroredLimitRange[]>>(url);
  return res.data.data ?? [];
}

// ============================================================
// Apiserver allow-list (migration 070)
// ============================================================

export type ApiserverAllowlistMode = 'monitor' | 'enforce' | 'disabled';
export type ApiserverAllowlistSyncStatus =
  | 'synced'
  | 'drifting'
  | 'pending'
  | 'failed';
export type ApiserverAllowlistProvider =
  | 'eks'
  | 'gke'
  | 'aks'
  | 'doks'
  | 'self_managed'
  | 'unknown';

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

export async function getApiserverAllowlist(
  clusterId: string,
): Promise<ApiserverAllowlistResponse> {
  const res = await api.get<APIResponse<ApiserverAllowlistResponse>>(
    `/clusters/${clusterId}/apiserver-allowlist/`,
  );
  return res.data.data;
}

export async function previewApiserverAllowlist(
  clusterId: string,
): Promise<ApiserverAllowlistResponse> {
  const res = await api.get<APIResponse<ApiserverAllowlistResponse>>(
    `/clusters/${clusterId}/apiserver-allowlist/preview/`,
  );
  return res.data.data;
}

export async function updateApiserverAllowlist(
  clusterId: string,
  body: ApiserverAllowlistUpdateRequest,
): Promise<ApiserverAllowlistResponse> {
  const res = await api.put<APIResponse<ApiserverAllowlistResponse>>(
    `/clusters/${clusterId}/apiserver-allowlist/`,
    body,
  );
  return res.data.data;
}

export async function reconcileApiserverAllowlist(
  clusterId: string,
): Promise<void> {
  await api.post(`/clusters/${clusterId}/apiserver-allowlist/reconcile/`);
}

export async function listApiserverAllowlistSnapshots(
  clusterId: string,
  opts?: { limit?: number; offset?: number },
): Promise<ApiserverAllowlistSnapshot[]> {
  const q = new URLSearchParams();
  if (opts?.limit !== undefined) q.set('limit', String(opts.limit));
  if (opts?.offset !== undefined) q.set('offset', String(opts.offset));
  const suffix = q.toString() ? `?${q.toString()}` : '';
  const res = await api.get<APIResponse<{ items: ApiserverAllowlistSnapshot[] }>>(
    `/clusters/${clusterId}/apiserver-allowlist/snapshots/${suffix}`,
  );
  return res.data.data.items ?? [];
}
// Service Mesh tile (migration 071)
//
// Three endpoints — current detection, on-demand re-detect, mTLS breakdown.
// All gated on clusters:read on the backend; the UI wraps them in the
// same auth context as the rest of the cluster-detail surface.
// ============================================================

export type ServiceMeshKind =
  | 'istio'
  | 'linkerd'
  | 'kuma'
  | 'cilium'
  | 'none'
  | 'unknown';

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

export async function getServiceMeshDetection(
  clusterId: string,
): Promise<ServiceMeshDetection> {
  const res = await api.get<APIResponse<ServiceMeshDetection>>(
    `/clusters/${clusterId}/service-mesh/`,
  );
  return res.data.data;
}

export async function reDetectServiceMesh(
  clusterId: string,
): Promise<ServiceMeshDetection> {
  const res = await api.post<APIResponse<ServiceMeshDetection>>(
    `/clusters/${clusterId}/service-mesh/detect/`,
  );
  return res.data.data;
}

export async function getServiceMeshMTLS(
  clusterId: string,
): Promise<MTLSBreakdown> {
  const res = await api.get<APIResponse<MTLSBreakdown>>(
    `/clusters/${clusterId}/service-mesh/mtls/`,
  );
  return res.data.data;
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
): Promise<ImageVulnHistoryResponse> {
  const q = new URLSearchParams();
  if (opts.sinceHours != null) q.set('since_hours', String(opts.sinceHours));
  if (opts.limit != null) q.set('limit', String(opts.limit));
  const suffix = q.toString() ? `?${q}` : '';
  const res = await api.get<{ data: Record<string, unknown> }>(
    `/clusters/${clusterId}/vulnerabilities/history/${suffix}`,
  );
  const raw = res.data.data;
  const snapshots = ((raw.snapshots as Record<string, unknown>[]) ?? []).map((s) => ({
    scannedAt: String(s.scanned_at ?? s.scannedAt ?? ''),
    critical: Number(s.critical ?? 0),
    high: Number(s.high ?? 0),
    medium: Number(s.medium ?? 0),
    low: Number(s.low ?? 0),
    unknown: Number(s.unknown ?? 0),
    reportCount: Number(s.report_count ?? s.reportCount ?? 0),
  }));
  return {
    clusterId: String(raw.cluster_id ?? raw.clusterId ?? ''),
    since: String(raw.since ?? ''),
    snapshots,
    totalCount: Number(raw.total_count ?? raw.totalCount ?? snapshots.length),
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
  delta?: { critical: number; high: number; medium: number; low: number; unknown: number };
}

export async function getImageVulnDiff(
  clusterId: string,
  priorHours = 24,
): Promise<ImageVulnDiff> {
  const res = await api.get<{ data: Record<string, unknown> }>(
    `/clusters/${clusterId}/vulnerabilities/diff/?prior_hours=${priorHours}`,
  );
  const raw = res.data.data;
  const bucket = (b: Record<string, unknown> | undefined): ImageVulnDiffBucket | undefined => {
    if (!b) return undefined;
    return {
      critical: Number(b.critical ?? 0),
      high: Number(b.high ?? 0),
      medium: Number(b.medium ?? 0),
      low: Number(b.low ?? 0),
      unknown: Number(b.unknown ?? 0),
      scannedAt: String(b.scanned_at ?? b.scannedAt ?? ''),
    };
  };
  return {
    clusterId: String(raw.cluster_id ?? raw.clusterId ?? ''),
    hasComparison: Boolean(raw.has_comparison ?? raw.hasComparison ?? false),
    priorHours: Number(raw.prior_hours ?? raw.priorHours ?? priorHours),
    latest: bucket(raw.latest as Record<string, unknown> | undefined),
    prior: bucket(raw.prior as Record<string, unknown> | undefined),
    delta: raw.delta as ImageVulnDiff['delta'],
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
): Promise<ImageVulnReportHistoryResponse> {
  const q = new URLSearchParams();
  if (opts.limit != null) q.set('limit', String(opts.limit));
  const suffix = q.toString() ? `?${q}` : '';
  const res = await api.get<{ data: Record<string, unknown> }>(
    `/clusters/${clusterId}/vulnerabilities/reports/${reportId}/history/${suffix}`,
  );
  const raw = res.data.data;
  const snapshots = ((raw.snapshots as Record<string, unknown>[]) ?? []).map((s) => ({
    scannedAt: String(s.scanned_at ?? s.scannedAt ?? ''),
    critical: Number(s.critical ?? 0),
    high: Number(s.high ?? 0),
    medium: Number(s.medium ?? 0),
    low: Number(s.low ?? 0),
    unknown: Number(s.unknown ?? 0),
  }));
  return {
    reportId: String(raw.report_id ?? raw.reportId ?? reportId),
    snapshots,
    totalCount: Number(raw.total_count ?? raw.totalCount ?? snapshots.length),
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

export async function getImageVulnProgress(clusterId: string): Promise<ImageVulnProgress> {
  const res = await api.get<{ data: Record<string, unknown> }>(
    `/clusters/${clusterId}/vulnerabilities/progress/`,
  );
  const raw = res.data.data ?? {};
  // Snake → camel mapping. If we leave the snake names exposed to the
  // page (`progress.trivy_operator_ready`) a typo'd accessor on the
  // camelCase form silently reads undefined and renders the wrong
  // banner state — which is exactly the "trivy not ready / 0 scans /
  // invalid date" bug operators were seeing.
  return {
    scanning: Boolean(raw.scanning ?? false),
    activeJobs: Number(raw.active_jobs ?? raw.activeJobs ?? 0),
    completedJobs: Number(raw.completed_jobs ?? raw.completedJobs ?? 0),
    failedJobs: Number(raw.failed_jobs ?? raw.failedJobs ?? 0),
    reportsCount: Number(raw.reports_count ?? raw.reportsCount ?? 0),
    trivyOperatorReady: Boolean(raw.trivy_operator_ready ?? raw.trivyOperatorReady ?? false),
    lastScanAgeSeconds:
      raw.last_scan_age_seconds != null
        ? Number(raw.last_scan_age_seconds)
        : raw.lastScanAgeSeconds != null
          ? Number(raw.lastScanAgeSeconds)
          : null,
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
  sourceKind: 'app' | 'tool';
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
): Promise<ClusterAppsResponse> {
  const q = new URLSearchParams();
  if (opts.limit != null) q.set('limit', String(opts.limit));
  if (opts.offset != null) q.set('offset', String(opts.offset));
  const suffix = q.toString() ? `?${q}` : '';
  const res = await api.get<{ data: Record<string, unknown>[]; count: number }>(
    `/clusters/${clusterId}/apps/${suffix}`,
  );
  const items: ClusterAppRow[] = (res.data.data ?? []).map((raw) => ({
    id: String(raw.id ?? ''),
    clusterId: String(raw.cluster_id ?? raw.clusterId ?? ''),
    chartId: String(raw.chart_id ?? raw.chartId ?? ''),
    chartVersionId: String(raw.chart_version_id ?? raw.chartVersionId ?? ''),
    releaseName: String(raw.release_name ?? raw.releaseName ?? ''),
    namespace: String(raw.namespace ?? ''),
    status: String(raw.status ?? ''),
    revision: Number(raw.revision ?? 0),
    valuesOverride: String(raw.values_override ?? raw.valuesOverride ?? ''),
    toolSlug: String(raw.tool_slug ?? raw.toolSlug ?? ''),
    presetUsed: String(raw.preset_used ?? raw.presetUsed ?? ''),
    sourceKind: ((raw.source_kind ?? raw.sourceKind ?? 'app') as ClusterAppRow['sourceKind']),
    displayName: String(raw.display_name ?? raw.displayName ?? ''),
    chartName: String(raw.chart_name ?? raw.chartName ?? ''),
    chartVersion: String(raw.chart_version ?? raw.chartVersion ?? ''),
    chartAppVersion: String(raw.chart_app_version ?? raw.chartAppVersion ?? ''),
    chartDescription: String(raw.chart_description ?? raw.chartDescription ?? ''),
    chartIconUrl: String(raw.chart_icon_url ?? raw.chartIconUrl ?? ''),
    chartCategory: String(raw.chart_category ?? raw.chartCategory ?? ''),
    repoName: String(raw.repo_name ?? raw.repoName ?? ''),
    repoType: String(raw.repo_type ?? raw.repoType ?? ''),
    createdAt: String(raw.created_at ?? raw.createdAt ?? ''),
    updatedAt: String(raw.updated_at ?? raw.updatedAt ?? ''),
  }));
  return { items, total: Number(res.data.count ?? items.length) };
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
  limit?: number;
  offset?: number;
  search?: string;
} = {}): Promise<{ items: CatalogChartSummary[]; total: number }> {
  const q = new URLSearchParams();
  if (params.limit != null) q.set('limit', String(params.limit));
  if (params.offset != null) q.set('offset', String(params.offset));
  if (params.search) q.set('search', params.search);
  const suffix = q.toString() ? `?${q}` : '';
  const res = await api.get<{ data: Record<string, unknown>[]; count: number }>(
    `/catalog/charts/${suffix}`,
  );
  const items: CatalogChartSummary[] = (res.data.data ?? []).map((raw) => ({
    id: String(raw.id ?? ''),
    repositoryId: String(raw.repository_id ?? raw.repositoryId ?? ''),
    name: String(raw.name ?? ''),
    displayName: String(raw.display_name ?? raw.displayName ?? raw.name ?? ''),
    description: String(raw.description ?? ''),
    iconUrl: String(raw.icon_url ?? raw.iconUrl ?? ''),
    homeUrl: String(raw.home_url ?? raw.homeUrl ?? ''),
    category: String(raw.category ?? ''),
    keywords: Array.isArray(raw.keywords) ? (raw.keywords as string[]) : [],
    deprecated: Boolean(raw.deprecated ?? false),
  }));
  return { items, total: Number(res.data.count ?? items.length) };
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
  limit = 10,
): Promise<RecommendedChart[]> {
  const res = await api.get<{ data: Record<string, unknown>[] }>(
    `/catalog/recommendations/popular/?limit=${limit}`,
  );
  return (res.data.data ?? []).map((raw) => ({
    chartId: String(raw.chart_id ?? raw.chartId ?? ''),
    name: String(raw.name ?? raw.chart_name ?? raw.chartName ?? ''),
    score: Number(raw.score ?? 0),
    ratingAvg: Number(raw.rating_avg ?? raw.ratingAvg ?? 0),
    installCount: Number(raw.install_count ?? raw.installCount ?? 0),
  }));
}

// Chart-version list for the install modal's version dropdown.
export interface ChartVersionRow {
  id: string;
  version: string;
  appVersion: string;
  createdAtUpstream: string;
}

export async function listChartVersions(chartId: string): Promise<ChartVersionRow[]> {
  const res = await api.get<{ data: Record<string, unknown>[] }>(
    `/catalog/charts/${chartId}/versions/?limit=50`,
  );
  return (res.data.data ?? []).map((raw) => ({
    id: String(raw.id ?? ''),
    version: String(raw.version ?? ''),
    appVersion: String(raw.app_version ?? raw.appVersion ?? ''),
    createdAtUpstream: String(raw.created_at_upstream ?? raw.createdAtUpstream ?? ''),
  }));
}

// Default values.yaml for the install modal's YAML editor pre-fill.
// First call on a given version triggers backend hydration (~1-2s);
// subsequent calls are cached in the DB row.
export async function getChartDefaultValues(
  chartId: string,
  version?: string,
): Promise<{ chart: string; version: string; defaultValues: string }> {
  const q = version ? `?version=${encodeURIComponent(version)}` : '';
  const res = await api.get<{ data: { chart: string; version: string; default_values: string } }>(
    `/catalog/charts/${chartId}/values/${q}`,
  );
  return {
    chart: res.data.data.chart,
    version: res.data.data.version,
    defaultValues: res.data.data.default_values ?? '',
  };
}

// Kick off a fresh install on this cluster. Returns the created
// installed_charts row id; the helm install itself happens
// asynchronously via the tunnel + worker queue.
export async function installChartOnCluster(req: {
  clusterId: string;
  chartVersionId: string;
  releaseName: string;
  namespace: string;
  valuesOverride: string;
}): Promise<{ id: string }> {
  const res = await api.post<{ data: { id: string } }>(`/catalog/installed/`, {
    cluster_id: req.clusterId,
    chart_version_id: req.chartVersionId,
    release_name: req.releaseName,
    namespace: req.namespace,
    values_override: req.valuesOverride,
  });
  return { id: res.data.data.id };
}

export async function uninstallCatalogRelease(installedChartId: string): Promise<void> {
  await api.delete(`/catalog/installed/${installedChartId}/`);
}

// Rancher-style bulk-delete of stuck releases. Backend hard-deletes any
// installed_charts rows in failed_install / failed_uninstall on this
// cluster and returns the affected row count.
export async function deleteFailedClusterApps(clusterId: string): Promise<{ deleted: number }> {
  const res = await api.delete<{ deleted: number }>(`/clusters/${clusterId}/apps/failed/`);
  return res.data ?? { deleted: 0 };
}
