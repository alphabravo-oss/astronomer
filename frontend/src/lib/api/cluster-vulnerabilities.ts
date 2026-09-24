/** Image-vulnerability reports, history, diffs, and rescan operations. */

import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import type { OperationSnapshot } from "@/lib/api/operation-polling";
import { mapPage } from "@/lib/api/pagination";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { PaginatedResponse } from "@/types";

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
  vulnerabilities: PaginatedResponse<CVERow>;
  severityFilter: string;
}

type ImageVulnReportWire =
  OpenAPIComponents["schemas"]["ImageVulnerabilityReport"];

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
  const raw = requireEnvelopeData(wire, "Vulnerability summary");
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
): Promise<PaginatedResponse<ImageVulnReport>> {
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
  return mapPage(wire, (raw) =>
    mapImageVulnReport({
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
    }),
  );
}

export async function getImageVulnReport(
  clusterId: string,
  reportId: string,
  opts: { severity?: CVESeverity; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<ImageVulnReportDetail> {
  const wire = await generated.getClustersByClusterIdVulnerabilitiesReportsById(
    {
      path: { cluster_id: clusterId, id: reportId },
      query: {
        severity: opts.severity,
        limit: opts.limit,
        offset: opts.offset,
      },
      signal,
    },
  );
  const raw = wire.data;
  return {
    report: mapImageVulnReport(raw.report),
    vulnerabilities: mapPage(raw.vulnerabilities, (row) => ({
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
    severityFilter: raw.severity_filter,
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
    path: { cluster_id: clusterId },
    query: { prior_hours: priorHours },
    signal,
  });
  const raw = wire.data;
  const bucket = (
    b: OpenAPIComponents["schemas"]["VulnerabilityDiffBucket"] | undefined,
  ): ImageVulnDiffBucket | undefined => {
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
  const wire =
    await generated.getClustersByClusterIdVulnerabilitiesReportsByReportIdHistory(
      {
        path: { cluster_id: clusterId, report_id: reportId },
        query: { limit: opts.limit },
        signal,
      },
    );
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
    path: { cluster_id: clusterId },
    signal,
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
