import {
  getSecurityProfiles,
  getSecurityScans,
  getSecurityScansById,
  postSecurityScans,
} from "@/lib/api/generated/client";
import { API_BASE } from "@/lib/env";
import type {
  CISFinding,
  CISProfilesResponse,
  CISScanCreatePayload,
  CISScanDetail,
  CISScanListItem,
  CISScanStatus,
  PaginatedResponse,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type CISWire = OpenAPIComponents["schemas"]["CISScan"];

function mapFinding(
  finding: OpenAPIComponents["schemas"]["CISFinding"],
): CISFinding {
  return {
    testId: finding.test_id,
    severity: finding.severity,
    status: finding.status,
    description: finding.description,
    remediation: finding.remediation,
  };
}

export function mapCISScan(wire: CISWire): CISScanDetail {
  return {
    id: wire.id,
    clusterId: wire.cluster_id,
    scanType: wire.scan_type,
    status: wire.status as CISScanStatus,
    passed: wire.passed,
    failed: wire.failed,
    warned: wire.warned,
    skipped: wire.skipped,
    startedAt: wire.started_at,
    completedAt: wire.completed_at,
    clusterScanName: wire.cluster_scan_name,
    initiatedById: wire.initiated_by_id ?? undefined,
    errorMessage: wire.terminal_reason,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
    findings: (wire.findings ?? []).map(mapFinding),
    summary: wire.summary,
    results: wire.results,
  };
}

function requireData<T>(value: T | undefined, operation: string): T {
  if (value === undefined) throw new Error(`${operation} returned no data`);
  return value;
}

export async function getCISProfiles(
  clusterId: string,
): Promise<CISProfilesResponse> {
  const response = await getSecurityProfiles({
    query: { cluster_id: clusterId },
  });
  return requireData(response.data, "get CIS profiles");
}

export async function getCISScans(params?: {
  page?: number;
  pageSize?: number;
  limit?: number;
  offset?: number;
}): Promise<PaginatedResponse<CISScanListItem>> {
  const limit = params?.limit ?? params?.pageSize;
  const page = Math.max(1, params?.page ?? 1);
  const offset =
    params?.offset ?? (limit === undefined ? undefined : (page - 1) * limit);
  const response = await getSecurityScans({ query: { limit, offset } });
  const data = (response.data ?? []).map(mapCISScan);
  const total = response.count ?? data.length;
  const pageSize = limit ?? Math.max(data.length, 1);
  return {
    data,
    count: total,
    total,
    next: response.next ?? null,
    previous: response.previous ?? null,
    page,
    pageSize,
    totalPages: Math.max(1, Math.ceil(total / pageSize)),
  };
}

export async function getCISScan(id: string): Promise<CISScanDetail> {
  const response = await getSecurityScansById({ path: { id } });
  return mapCISScan(response.data);
}

export async function createCISScan(
  payload: CISScanCreatePayload,
): Promise<CISScanDetail> {
  const response = await postSecurityScans({ body: payload });
  return mapCISScan(response.data);
}

export function cisScanReportCSVUrl(id: string): string {
  return `${API_BASE}/security/scans/${encodeURIComponent(id)}/report.csv`;
}
