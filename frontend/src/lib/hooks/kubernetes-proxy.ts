import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import * as apiClient from "@/lib/api";
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
      apiClient.getGenericResources(clusterId, resourceType, signal),
    enabled: !!clusterId && !!resourceType,
    refetchInterval: liveFallback(30000),
  });
}

export function useResourceDiscovery(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.generic.discovery(clusterId),
    queryFn: () => apiClient.getResourceDiscovery(clusterId),
    enabled: !!clusterId,
    staleTime: 5 * 60_000,
  });
}

export function useResourceSchema(
  clusterId: string,
  resourceType: apiClient.ResourceType,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.generic.schema(clusterId, resourceType),
    queryFn: () => apiClient.getResourceSchema(clusterId, resourceType),
    enabled: enabled && !!clusterId && !!resourceType,
    staleTime: 5 * 60_000,
  });
}

// ============================================================
// Kubeconfig Hook
// ============================================================

export function useDownloadProxyKubeconfig() {
  return useMutation({
    mutationFn: (clusterId: string) =>
      apiClient.downloadProxyKubeconfig(clusterId),
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
    mutationFn: (clusterId: string) =>
      apiClient.downloadDirectKubeconfig(clusterId),
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
    queryFn: () => apiClient.k8sGetYaml(clusterId, path),
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
    queryFn: () => apiClient.k8sGet(clusterId, path),
    enabled: !!clusterId && !!path && enabled,
    staleTime: 0,
    gcTime: 0,
  });
}

export function useK8sDelete() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path }: { clusterId: string; path: string }) =>
      apiClient.k8sDelete(clusterId, path),
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
    }) => apiClient.k8sApplyYaml(clusterId, path, yaml, force),
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
    }) => apiClient.k8sDryRunYaml(clusterId, path, yaml),
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
    }) => apiClient.k8sCreate(clusterId, path, body),
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
    }) => apiClient.k8sPatch(clusterId, path, body, patchType),
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
