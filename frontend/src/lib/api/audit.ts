import { getActivity, listAuditLogs } from "@/lib/api/generated/client";
import { API_BASE } from "@/lib/env";
import type { ActivityEvent, AuditLogEntry, PaginatedResponse } from "@/types";
import type {
  OpenAPIComponents,
  OpenAPIOperations,
} from "@/types/openapi.generated";

type AuditLogWire = OpenAPIComponents["schemas"]["AuditLogEntry"];
type AuditListQuery = NonNullable<
  OpenAPIOperations["listAuditLogs"]["arguments"]["query"]
>;

export type AuditLogQueryParams = {
  page?: number;
  pageSize?: number;
  limit?: number;
  offset?: number;
  q?: string;
  actor?: string;
  action?: string;
  user?: string;
  user_id?: string;
  target?: string;
  resource_type?: string;
  resource_id?: string;
  resource_name?: string;
  cluster_id?: string;
  project_id?: string;
  result?: "success" | "failure" | "error" | string;
  status_code?: number;
  source?: string;
  correlation_id?: string;
  request_id?: string;
  from?: string;
  to?: string;
  action_class?: string;
  audience?: "people" | "system" | "all" | string;
};

function auditLogRequestParams(
  params?: AuditLogQueryParams,
): AuditListQuery | undefined {
  if (!params) return undefined;
  const limit = params.limit ?? params.pageSize;
  const page = params.page ?? 1;
  const offset =
    params.offset ?? (limit ? Math.max(0, page - 1) * limit : undefined);
  return {
    limit,
    offset,
    q: params.q || undefined,
    actor: params.actor || params.user || undefined,
    user_id: params.user_id || undefined,
    action: params.action || undefined,
    action_class: params.action_class || undefined,
    audience: (params.audience || undefined) as AuditListQuery["audience"],
    target: params.target || undefined,
    resource_type: params.resource_type || undefined,
    resource_id: params.resource_id || undefined,
    resource_name: params.resource_name || undefined,
    cluster_id: params.cluster_id || undefined,
    project_id: params.project_id || undefined,
    result: (params.result || undefined) as AuditListQuery["result"],
    status_code: params.status_code,
    source: params.source || undefined,
    correlation_id: params.correlation_id || undefined,
    request_id: params.request_id || undefined,
    from: params.from || undefined,
    to: params.to || undefined,
  };
}

function mapAuditLog(wire: AuditLogWire): AuditLogEntry {
  const detail =
    wire.detail && typeof wire.detail === "object"
      ? (wire.detail as Record<string, unknown>)
      : undefined;
  const details =
    wire.details && typeof wire.details === "object"
      ? (wire.details as Record<string, unknown>)
      : undefined;
  return {
    id: wire.id ?? "",
    userId: wire.user_id,
    user: wire.user ?? "system",
    action: wire.action ?? "",
    actionClass: wire.action_class as AuditLogEntry["actionClass"],
    resourceType: wire.resource_type ?? "",
    resourceId: wire.resource_id,
    resourceName: wire.resource_name ?? "",
    source: wire.source,
    correlationId: wire.correlation_id,
    actorAuthMethod: wire.actor_auth_method,
    httpMethod: wire.http_method,
    path: wire.path,
    statusCode: wire.status_code,
    durationMs: wire.duration_ms,
    requestId: wire.request_id,
    ipAddress: wire.ip_address,
    userAgent: wire.user_agent,
    sourceIP: wire.source_ip ?? "",
    status: (wire.status ?? "error") as AuditLogEntry["status"],
    detail,
    details,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
    timestamp: wire.timestamp ?? wire.created_at ?? "",
  };
}

export async function getAuditLogs(
  params?: AuditLogQueryParams,
): Promise<PaginatedResponse<AuditLogEntry>> {
  const query = auditLogRequestParams(params);
  const response = await listAuditLogs({ query });
  const pageSize = query?.limit ?? 20;
  const offset = query?.offset ?? 0;
  return {
    data: (response.data ?? []).map(mapAuditLog),
    total: response.count,
    count: response.count,
    next: response.next,
    previous: response.previous,
    page: pageSize > 0 ? Math.floor(offset / pageSize) + 1 : 1,
    pageSize,
    totalPages:
      pageSize > 0 ? Math.max(1, Math.ceil(response.count / pageSize)) : 1,
  };
}

export function getAuditLogExportURL(params?: AuditLogQueryParams) {
  const requestParams = {
    ...auditLogRequestParams(params),
    format: "csv" as const,
  };
  const search = new URLSearchParams();
  Object.entries(requestParams).forEach(([key, value]) =>
    search.set(key, String(value)),
  );
  return `${API_BASE}/audit/export/?${search.toString()}`;
}

export async function getActivityFeed(params?: { limit?: number }) {
  const response = await getActivity({ query: params });
  return response.map((wire): ActivityEvent => ({
    id: wire.id ?? "",
    type: (wire.type ?? "system") as ActivityEvent["type"],
    action: wire.action ?? "",
    message: wire.message ?? "",
    resource: wire.resource,
    timestamp: wire.timestamp ?? "",
  }));
}
