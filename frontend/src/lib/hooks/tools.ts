import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  adoptTool,
  getClusterToolsStatus,
  getToolOperation,
  getTools,
  installTool,
  uninstallTool,
} from "@/lib/api/tools";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

const TOOL_OP_TERMINAL = new Set(["completed", "failed", "superseded"]);

export function useTools() {
  return useQuery({
    queryKey: queryKeys.tools.list(),
    queryFn: getTools,
    staleTime: 60_000,
  });
}

export function useClusterToolsStatus(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.tools.clusterStatus(clusterId),
    queryFn: () => getClusterToolsStatus(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30_000),
  });
}

export function useToolOperation(operationId: string | null) {
  return useQuery({
    queryKey: queryKeys.tools.operation(operationId || ""),
    queryFn: () => getToolOperation(operationId as string),
    enabled: !!operationId,
    refetchInterval: (query) =>
      query.state.data?.status && TOOL_OP_TERMINAL.has(query.state.data.status)
        ? false
        : liveFallback(2_000)(),
  });
}

export function useInstallTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      slug,
      ...data
    }: {
      slug: string;
      cluster_id: string;
      preset: string;
      values_override?: string;
    }) => installTool(slug, data),
    onSuccess: (_, { cluster_id }) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.tools.clusterStatus(cluster_id),
      });
      toastSuccess("Tool installation initiated");
    },
    onError: (error: Error) => toastApiError("Failed to install tool", error),
  });
}

export function useUninstallTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ slug, cluster_id }: { slug: string; cluster_id: string }) =>
      uninstallTool(slug, { cluster_id }),
    onSuccess: (_, { cluster_id }) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.tools.clusterStatus(cluster_id),
      });
      toastSuccess("Tool uninstall initiated");
    },
    onError: (error: Error) => toastApiError("Failed to uninstall tool", error),
  });
}

export function useAdoptTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      slug,
      ...data
    }: {
      slug: string;
      cluster_id: string;
      release_name: string;
    }) => adoptTool(slug, data),
    onSuccess: (_, { cluster_id }) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.tools.clusterStatus(cluster_id),
      });
      toastSuccess("Tool adopted successfully");
    },
    onError: (error: Error) => toastApiError("Failed to adopt tool", error),
  });
}
