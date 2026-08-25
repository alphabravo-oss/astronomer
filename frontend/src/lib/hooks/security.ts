import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  applySecurityPolicy,
  assignSecurityPolicy,
  createPodSecurityTemplate,
  deletePodSecurityTemplate,
  getClusterSecurityPolicies,
  getPodSecurityTemplates,
  removeSecurityPolicy,
  updatePodSecurityTemplate,
} from "@/lib/api/security-policies";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { PodSecurityTemplate } from "@/types";

export function usePodSecurityTemplates() {
  return useQuery({
    queryKey: queryKeys.security.templates,
    queryFn: getPodSecurityTemplates,
  });
}

export function useCreatePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<PodSecurityTemplate>) =>
      createPodSecurityTemplate(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess("PSA template created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create template", error);
    },
  });
}

export function useUpdatePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: Partial<PodSecurityTemplate>;
    }) => updatePodSecurityTemplate(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess("PSA template updated");
    },
    onError: (error: Error) => {
      toastApiError("Failed to update template", error);
    },
  });
}

export function useDeletePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: deletePodSecurityTemplate,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess("PSA template deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete template", error);
    },
  });
}

export function useClusterSecurityPolicies() {
  return useQuery({
    queryKey: queryKeys.security.policies,
    queryFn: getClusterSecurityPolicies,
    refetchInterval: liveFallback(30000),
  });
}

export function useAssignSecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: assignSecurityPolicy,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess("Security policy assigned");
    },
    onError: (error: Error) => {
      toastApiError("Failed to assign policy", error);
    },
  });
}

export function useApplySecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: applySecurityPolicy,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess("Security policy applied to cluster");
    },
    onError: (error: Error) => {
      toastApiError("Failed to apply policy", error);
    },
  });
}

export function useRemoveSecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: removeSecurityPolicy,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess("Security policy removed");
    },
    onError: (error: Error) => {
      toastApiError("Failed to remove policy", error);
    },
  });
}
