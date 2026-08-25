import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import * as apiClient from "@/lib/api";
import { getCharlieActivation } from "@/lib/api/charlie-admin";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { ClusterRegistration } from "@/types";
import type { UpdateClusterInput } from "@/lib/api/clusters";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";

export function useFeatureFlags() {
  return useQuery({
    queryKey: queryKeys.featureFlags,
    queryFn: () => apiClient.getFeatureFlags(),
    staleTime: 30_000,
  });
}

export function useCharlieActivated() {
  const flags = useFeatureFlags();
  const enabled = flags.data?.["feature.charlie"] === true;
  const activation = useQuery({
    queryKey: queryKeys.charlie.activation,
    queryFn: ({ signal }) => getCharlieActivation(signal),
    enabled,
    retry: false,
    staleTime: 15_000,
  });
  return {
    featureEnabled: enabled,
    activated: enabled && activation.data?.activated === true,
    endpoint: activation.data?.endpoint,
    isLoading:
      flags.isLoading || (enabled && activation.isLoading && !activation.data),
  };
}

// ============================================================
// Cluster Hooks
// ============================================================

export function useClusters(params?: {
  status?: string;
  provider?: string;
  environment?: string;
  search?: string;
  page?: number;
  pageSize?: number;
}) {
  return useQuery({
    queryKey: queryKeys.clusters.list(params),
    queryFn: () => apiClient.getClusters(params),
    // Poll only while the live bus is down; events drive freshness when open.
    refetchInterval: liveFallback(30000),
  });
}

export function useCluster(id: string) {
  return useQuery({
    queryKey: queryKeys.clusters.detail(id),
    queryFn: () => apiClient.getCluster(id),
    enabled: !!id,
    refetchInterval: liveFallback(15000),
  });
}

export function useClusterNodes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.nodes(clusterId),
    queryFn: ({ signal }) => apiClient.getClusterNodes(clusterId, { signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

// The health-check worker reconciles conditions every 60s; while the stream
// is open the `cluster.status_changed` + heartbeat routes refresh this
// (P4.9) and the poll only backstops a dropped stream.
export function useClusterConditions(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.conditions(clusterId),
    queryFn: ({ signal }) =>
      apiClient.getClusterConditions(clusterId, { signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(60000),
  });
}

// Sprint 086 — remediation history feeds the "Last action" footer
// under the condition pills. Routed from the same status/heartbeat
// events as conditions (P4.9); the poll backstops a dropped stream.
export function useClusterConditionRemediation(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.conditionRemediation(clusterId),
    queryFn: ({ signal }) =>
      apiClient.getClusterConditionRemediation(clusterId, { signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

export function useNodeDetail(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: queryKeys.clusters.nodeDetail(clusterId, nodeName),
    queryFn: ({ signal }) =>
      apiClient.getNodeDetail(clusterId, nodeName, { signal }),
    enabled: !!clusterId && !!nodeName,
    refetchInterval: liveFallback(30000),
  });
}

export function useClusterNamespaces(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.namespaces(clusterId),
    queryFn: ({ signal }) => apiClient.getClusterNamespaces(clusterId, signal),
    enabled: !!clusterId,
  });
}

export function useClusterEvents(
  clusterId: string,
  params?: { limit?: number },
) {
  return useQuery({
    queryKey: queryKeys.clusters.events(
      clusterId,
      params as Record<string, unknown> | undefined,
    ),
    queryFn: ({ signal }) =>
      apiClient.getClusterEvents(clusterId, { ...params, signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
  });
}

export function useClusterPods(
  clusterId: string,
  params?: { namespace?: string },
) {
  return useQuery({
    queryKey: queryKeys.clusters.pods(clusterId, params),
    queryFn: ({ signal }) =>
      apiClient.getClusterPods(clusterId, { ...params, signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
  });
}

export function useCreateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: ClusterRegistration) => apiClient.createCluster(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      toastSuccess("Cluster registration initiated");
    },
    onError: (error: Error) => {
      toastApiError("Failed to register cluster", error);
    },
  });
}

export function useUpdateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateClusterInput }) =>
      apiClient.updateCluster(id, data),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusters.detail(variables.id),
      });
      toastSuccess("Cluster updated");
    },
    onError: (error: Error) => {
      toastApiError("Failed to update cluster", error);
    },
  });
}

export function useTakeoverClusterOwnership() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.takeoverClusterOwnership(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusters.detail(id),
      });
      toastSuccess("Cluster ownership transferred");
    },
    onError: (error: Error) => {
      toastApiError("Failed to transfer cluster ownership", error);
    },
  });
}

export function useDeleteCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (arg: string | { id: string; force?: boolean }) => {
      const { id, force } =
        typeof arg === "string" ? { id: arg, force: false } : arg;
      return apiClient.deleteCluster(id, { force });
    },
    onSuccess: () => {
      // Decommission is async: DELETE returns 202 and the worker tombstones the
      // row when cleanup finishes (instant for a disconnected agent's record;
      // up to the grace window if it waits for an agent to reconnect). The row
      // STAYS in the list, flagged `decommissioning`, so the dashboard shows a
      // stable "Decommissioning" badge — no optimistic hide/re-show flicker. It
      // drops out on its own once tombstoned: `cluster.deleted` +
      // cluster lifecycle events cover the tombstone window,
      // so a single refresh here just makes the badge appear immediately.
      toastSuccess("Cluster decommissioning started");
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete cluster", error);
    },
  });
}

export function useDeletePod() {
  const queryClient = useQueryClient();
  return useOperationMutation({
    keyPrefix: "pod-delete",
    submit: (
      {
        clusterId,
        namespace,
        name,
      }: {
        clusterId: string;
        namespace: string;
        name: string;
      },
      context,
    ) => apiClient.deletePod(clusterId, namespace, name, context),
    read: apiClient.getPodDeleteOperation,
    mutation: {
      onSuccess: (_data, variables) => {
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusters.podsAll(variables.clusterId),
        });
        toastSuccess("Pod deleted");
      },
      onError: (error: Error) => {
        toastApiError("Failed to delete pod", error);
      },
    },
  });
}

export function useNodeOperation() {
  const queryClient = useQueryClient();
  return useOperationMutation({
    keyPrefix: "node-operation",
    submit: apiClient.submitNodeOperation,
    read: apiClient.getNodeOperationStatus,
    mutation: {
      onSuccess: (operation) => {
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusters.nodes(operation.clusterId),
        });
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusters.nodeDetail(
            operation.clusterId,
            operation.nodeName,
          ),
        });
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusters.podsAll(operation.clusterId),
        });
      },
    },
  });
}
