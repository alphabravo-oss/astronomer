import { useQuery } from "@tanstack/react-query";

import * as apiClient from "@/lib/api/audit";
import type { AuditLogQueryParams } from "@/lib/api/audit";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";

export function useAuditLogs(
  params?: AuditLogQueryParams,
  options?: { enabled?: boolean },
) {
  return useQuery({
    queryKey: queryKeys.settings.auditLogs(params),
    queryFn: ({ signal }) => apiClient.getAuditLogs(params, signal),
    enabled: options?.enabled,
    throwOnError: false,
  });
}

// ============================================================
// Activity Feed Hook
// ============================================================

export function useActivityFeed(limit: number = 20) {
  return useQuery({
    queryKey: queryKeys.activity(limit),
    queryFn: ({ signal }) => apiClient.getActivityFeed({ limit }, signal),
    // `audit.*` events refresh this while the stream is open. Restricted
    // users never receive them (no cluster_id → SEC-R07 fail-closed drop);
    // they heal via this fallback poll + the reconnect bulk invalidation.
    refetchInterval: liveFallback(30000),
  });
}
