import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  getGenericResources,
  getClusterResourceCounts,
} from "@/lib/api/resource-search";
import {
  getResourceDiscovery,
  ResourceType,
  getResourceSchema,
} from "@/lib/api/resources";
import {
  downloadProxyKubeconfig,
  downloadDirectKubeconfig,
  k8sGetYaml,
  k8sGet,
  k8sDelete,
  k8sApplyYaml,
  k8sDryRunYaml,
  k8sCreate,
  k8sPatch,
} from "@/lib/api/kubernetes-proxy";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

// ============================================================
// Generic K8s Resource Hook
// ============================================================

export function useGenericResources(clusterId: string, resourceType: string) {
  return useQuery({
    queryKey: queryKeys.generic.resources(clusterId, resourceType),
    queryFn: ({ signal }) =>
      getGenericResources(clusterId, resourceType, signal),
    enabled: !!clusterId && !!resourceType,
    refetchInterval: liveFallback(30000),
  });
}

export function useClusterResourceCounts(
  clusterId: string,
  resourceTypes: readonly string[],
  namespaces: readonly string[] | null,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.generic.resourceCounts(
      clusterId,
      resourceTypes,
      namespaces,
    ),
    queryFn: ({ signal }) =>
      getClusterResourceCounts(clusterId, resourceTypes, namespaces, signal),
    enabled:
      enabled &&
      !!clusterId &&
      resourceTypes.length > 0 &&
      (namespaces === null || namespaces.length > 0),
    refetchInterval: liveFallback(30_000),
  });
}

export function useResourceDiscovery(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.generic.discovery(clusterId),
    queryFn: ({ signal }) => getResourceDiscovery(clusterId, signal),
    enabled: !!clusterId,
    staleTime: 5 * 60_000,
  });
}

export function useResourceSchema(
  clusterId: string,
  resourceType: ResourceType,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.generic.schema(clusterId, resourceType),
    queryFn: ({ signal }) => getResourceSchema(clusterId, resourceType, signal),
    enabled: enabled && !!clusterId && !!resourceType,
    staleTime: 5 * 60_000,
  });
}

// ============================================================
// Kubeconfig Hook
// ============================================================

export function useDownloadProxyKubeconfig() {
  return useMutation({
    mutationFn: (clusterId: string) => downloadProxyKubeconfig(clusterId),
    onSuccess: () => {
      toastSuccess("Read-only proxy kubeconfig downloaded");
    },
    onError: (error: Error) => {
      toastApiError("Failed to generate kubeconfig", error);
    },
  });
}

export function useDownloadDirectKubeconfig() {
  return useMutation({
    mutationFn: (clusterId: string) => downloadDirectKubeconfig(clusterId),
    onSuccess: () => {
      toastSuccess("15-minute direct read-only kubeconfig downloaded");
    },
    onError: (error: Error) => {
      toastApiError("Failed to generate direct kubeconfig", error);
    },
  });
}

// ============================================================
// K8s Proxy Hooks
// ============================================================

export const k8sQueryKeys = {
  yaml: (clusterId: string, path: string) =>
    ["k8s", clusterId, "yaml", path] as const,
  resource: (clusterId: string, path: string) =>
    ["k8s", clusterId, "resource", path] as const,
};

export function useK8sGetYaml(clusterId: string, path: string, enabled = true) {
  return useQuery({
    queryKey: k8sQueryKeys.yaml(clusterId, path),
    queryFn: ({ signal }) => k8sGetYaml(clusterId, path, signal),
    enabled: !!clusterId && !!path && enabled,
    staleTime: 0,
    gcTime: 0,
  });
}

// Single object as JSON (mirrors useK8sGetYaml). ponytail: reuse existing k8sQueryKeys.resource.
export function useK8sResource(
  clusterId: string,
  path: string,
  enabled = true,
) {
  return useQuery({
    queryKey: k8sQueryKeys.resource(clusterId, path),
    queryFn: ({ signal }) => k8sGet(clusterId, path, signal),
    enabled: !!clusterId && !!path && enabled,
    staleTime: 0,
    gcTime: 0,
  });
}

export function useK8sDelete() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path }: { clusterId: string; path: string }) =>
      k8sDelete(clusterId, path),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess("Resource deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete resource", error);
    },
  });
}

export function useK8sApplyYaml() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      path,
      yaml,
      force,
    }: {
      clusterId: string;
      path: string;
      yaml: string;
      force?: boolean;
    }) => k8sApplyYaml(clusterId, path, yaml, force),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess("Resource updated");
    },
    onError: (error: Error) => {
      toastApiError("Failed to update resource", error);
    },
  });
}

export function useK8sDryRunYaml() {
  return useMutation({
    mutationFn: ({
      clusterId,
      path,
      yaml,
    }: {
      clusterId: string;
      path: string;
      yaml: string;
    }) => k8sDryRunYaml(clusterId, path, yaml),
    onSuccess: () => {
      toastSuccess("Dry run passed");
    },
    onError: (error: Error) => {
      toastApiError("Dry run failed", error);
    },
  });
}

export function useK8sCreate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      path,
      body,
    }: {
      clusterId: string;
      path: string;
      body: unknown;
    }) => k8sCreate(clusterId, path, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess("Resource created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create resource", error);
    },
  });
}

export interface K8sCreateBatchItem {
  id: string;
  path: string;
  body: unknown;
  label: string;
}

export interface K8sCreateBatchResult extends K8sCreateBatchItem {
  ok: boolean;
  error?: unknown;
}

/**
 * Create Kubernetes objects in document order and retain an independent result
 * for each one. Sequential execution is intentional: manifests commonly place
 * namespaces and config ahead of the workloads that consume them.
 */
export function useK8sCreateBatch() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      clusterId,
      items,
    }: {
      clusterId: string;
      items: K8sCreateBatchItem[];
    }): Promise<K8sCreateBatchResult[]> => {
      const results: K8sCreateBatchResult[] = [];
      for (const item of items) {
        try {
          await k8sCreate(clusterId, item.path, item.body);
          results.push({ ...item, ok: true });
        } catch (error) {
          results.push({ ...item, ok: false, error });
        }
      }
      return results;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
    },
  });
}

export function useK8sPatch() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      path,
      body,
      patchType = "strategic-merge",
    }: {
      clusterId: string;
      path: string;
      body: unknown;
      patchType?: "strategic-merge" | "merge" | "json";
    }) => k8sPatch(clusterId, path, body, patchType),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess("Resource updated");
    },
    onError: (error: Error) => {
      toastApiError("Failed to patch resource", error);
    },
  });
}
