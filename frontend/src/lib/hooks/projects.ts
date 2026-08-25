import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import * as apiClient from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { Project } from "@/types";

// ============================================================
// Project Hooks
// ============================================================

export function useProjects(params?: { page?: number; pageSize?: number }) {
  return useQuery({
    queryKey: queryKeys.projects.list(params),
    queryFn: ({ signal }) => apiClient.getProjects(params, { signal }),
  });
}

export function useProject(id: string) {
  return useQuery({
    queryKey: queryKeys.projects.detail(id),
    queryFn: ({ signal }) => apiClient.getProject(id, { signal }),
    enabled: !!id,
  });
}

export function useCreateProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<Project>) => apiClient.createProject(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      toastSuccess("Project created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create project", error);
    },
  });
}

export function useDeleteProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteProject(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      toastSuccess("Project deleted");
    },
    onError: (error: Error) => {
      toastApiError("Failed to delete project", error);
    },
  });
}

export function useTakeoverProjectOwnership() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.takeoverProjectOwnership(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      queryClient.invalidateQueries({
        queryKey: queryKeys.projects.detail(id),
      });
      toastSuccess("Project ownership transferred");
    },
    onError: (error: Error) => {
      toastApiError("Failed to transfer project ownership", error);
    },
  });
}
