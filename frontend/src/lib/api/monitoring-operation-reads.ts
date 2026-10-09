import { requireMonitoringData as requireData } from "./monitoring-data";
import * as generated from "@/lib/api/generated/client";
import type {
  MonitoringOperation,
  MonitoringOperationStatus,
  MonitoringOperationTargetType,
} from "./monitoring-stack";

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
  signal?: AbortSignal,
): Promise<MonitoringOperation[]> {
  const response = await generated.getSettingsMonitoringOperations({
    signal,
    query: params,
  });
  return response.data ?? [];
}

/** Detail — the only endpoint that returns the operation's stage events. */
export async function getMonitoringOperation(
  id: string,
  signal?: AbortSignal,
): Promise<MonitoringOperation> {
  return requireData(
    await generated.getSettingsMonitoringOperationsById({
      signal,
      path: { id },
    }),
    "getMonitoringOperation",
  );
}
