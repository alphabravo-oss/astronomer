import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import * as apiClient from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { PersistentVolumeClaim } from "@/types";

// ============================================================
// Storage Hooks
// ============================================================

export function usePersistentVolumes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.storage.pvs(clusterId),
    queryFn: () => apiClient.getPersistentVolumes(clusterId),
    enabled: !!clusterId,
  });
}

export function usePersistentVolumeClaims(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.storage.pvcs(clusterId),
    queryFn: () => apiClient.getPersistentVolumeClaims(clusterId),
    enabled: !!clusterId,
  });
}

export function useStorageClasses(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.storage.storageClasses(clusterId),
    queryFn: () => apiClient.getStorageClasses(clusterId),
    enabled: !!clusterId,
  });
}

export function useCreatePVC() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      data,
    }: {
      clusterId: string;
      data: Partial<PersistentVolumeClaim>;
    }) => apiClient.createPersistentVolumeClaim(clusterId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      toastSuccess("PVC created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create PVC", error);
    },
  });
}

export function useDeletePVC() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      namespace,
      name,
    }: {
      clusterId: string;
      namespace: string;
      name: string;
    }) => apiClient.deletePersistentVolumeClaim(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      toastSuccess("PVC deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete PVC", error);
    },
  });
}

export function useDeletePV() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, name }: { clusterId: string; name: string }) =>
      apiClient.deletePersistentVolume(clusterId, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      toastSuccess("PV deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete PV", error);
    },
  });
}

// ============================================================
// Networking Hooks
// ============================================================

export function useServices(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.services(clusterId),
    queryFn: () => apiClient.getServices(clusterId),
    enabled: !!clusterId,
  });
}

export function useIngresses(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.ingresses(clusterId),
    queryFn: () => apiClient.getIngresses(clusterId),
    enabled: !!clusterId,
  });
}

export function useNetworkPolicies(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.networkPolicies(clusterId),
    queryFn: () => apiClient.getNetworkPolicies(clusterId),
    enabled: !!clusterId,
  });
}

export function useDeleteService() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      namespace,
      name,
    }: {
      clusterId: string;
      namespace: string;
      name: string;
    }) => apiClient.deleteService(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      toastSuccess("Service deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete service", error);
    },
  });
}

export function useDeleteIngress() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      namespace,
      name,
    }: {
      clusterId: string;
      namespace: string;
      name: string;
    }) => apiClient.deleteIngress(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      toastSuccess("Ingress deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete ingress", error);
    },
  });
}

export function useDeleteNetworkPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      namespace,
      name,
    }: {
      clusterId: string;
      namespace: string;
      name: string;
    }) => apiClient.deleteNetworkPolicy(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      toastSuccess("Network policy deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete network policy", error);
    },
  });
}

// ============================================================
// Gateway API Hooks
// ============================================================
//
// Read hooks only. Deletes and YAML edits in callers go through the generic
// useK8sDelete / useK8sApplyYaml above (with k8sResourcePath from
// lib/k8s-paths.ts), so we don't need per-resource mutations here.

export function useGateways(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.gateways(clusterId),
    queryFn: () => apiClient.getGateways(clusterId),
    enabled: !!clusterId,
  });
}

export function useHTTPRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.httpRoutes(clusterId),
    queryFn: () => apiClient.getHTTPRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useGatewayClasses(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.gatewayClasses(clusterId),
    queryFn: () => apiClient.getGatewayClasses(clusterId),
    enabled: !!clusterId,
  });
}

export function useGRPCRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.grpcRoutes(clusterId),
    queryFn: () => apiClient.getGRPCRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useTLSRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.tlsRoutes(clusterId),
    queryFn: () => apiClient.getTLSRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useTCPRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.tcpRoutes(clusterId),
    queryFn: () => apiClient.getTCPRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useUDPRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.udpRoutes(clusterId),
    queryFn: () => apiClient.getUDPRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useReferenceGrants(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.referenceGrants(clusterId),
    queryFn: () => apiClient.getReferenceGrants(clusterId),
    enabled: !!clusterId,
  });
}
