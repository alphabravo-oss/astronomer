"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createCISScan,
  getCISProfiles,
  getCISScan,
  getCISScans,
} from "@/lib/api/security-scans";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { CISScanCreatePayload } from "@/types";

const cisQueryKeys = {
  profiles: (clusterId: string) => ["cis", "profiles", clusterId] as const,
  scans: (params?: Record<string, unknown>) =>
    ["cis", "scans", params] as const,
  scan: (id: string) => ["cis", "scans", "detail", id] as const,
};

export function useCISProfiles(clusterId: string | undefined) {
  return useQuery({
    queryKey: cisQueryKeys.profiles(clusterId ?? ""),
    queryFn: () => getCISProfiles(clusterId!),
    enabled: !!clusterId,
    staleTime: 60_000,
  });
}

export function useCISScans(params?: {
  page?: number;
  pageSize?: number;
  limit?: number;
  offset?: number;
}) {
  return useQuery({
    queryKey: cisQueryKeys.scans(params),
    queryFn: () => getCISScans(params),
    refetchInterval: liveFallback(30_000),
  });
}

export function useCISScan(id: string | undefined) {
  return useQuery({
    queryKey: cisQueryKeys.scan(id ?? ""),
    queryFn: () => getCISScan(id!),
    enabled: !!id,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      if (
        status === "completed" ||
        status === "failed" ||
        status === "cancelled"
      ) {
        return false;
      }
      return liveFallback(10_000)();
    },
  });
}

export function useCreateCISScan() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: CISScanCreatePayload) => createCISScan(payload),
    onSuccess: (scan) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.cis.scansAll });
      queryClient.setQueryData(cisQueryKeys.scan(scan.id), scan);
      toastSuccess("CIS scan queued");
    },
    onError: (error: Error) => {
      toastApiError("Failed to start scan", error);
    },
  });
}
