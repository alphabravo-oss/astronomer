import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import {
  getProjects,
  getClusterProjects,
  getProject,
  createProject,
  deleteProject,
  takeoverProjectOwnership,
} from "@/lib/api/projects";
import type { ProjectListParams } from "@/lib/api/projects";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { Project } from "@/types";

// ============================================================
// Project Hooks
// ============================================================

export function useProjects(params?: ProjectListParams) {
  return useQuery({
    queryKey: queryKeys.projects.list(params ? { ...params } : undefined),
    queryFn: ({ signal }) => getProjects(params, { signal }),
  });
}

/** Bounded, cluster-scoped project search for pickers and command surfaces. */
export function useProjectSearch(
  clusterId: string,
  search: string,
  enabled = true,
) {
  const term = search.trim();
  return useInfiniteQuery({
    queryKey: queryKeys.projects.search(clusterId, term),
    initialPageParam: 1,
    queryFn: ({ pageParam, signal }) =>
      getClusterProjects(
        clusterId,
        { search: term, page: pageParam, pageSize: 50 },
        { signal },
      ),
    getNextPageParam: (page) => {
      const {
        has_more: hasMore,
        limit,
        next_offset: nextOffset,
      } = page.pagination;
      return hasMore && nextOffset !== null && limit > 0
        ? Math.floor(nextOffset / limit) + 1
        : undefined;
    },
    enabled: enabled && clusterId !== "",
    staleTime: 30_000,
  });
}

export function useProject(id: string, options: { throwOnError?: false } = {}) {
  return useQuery({
    queryKey: queryKeys.projects.detail(id),
    queryFn: ({ signal }) => getProject(id, { signal }),
    enabled: !!id,
    ...options,
  });
}

export function useCreateProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<Project>) => createProject(data),
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
    mutationFn: (id: string) => deleteProject(id),
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
    mutationFn: (id: string) => takeoverProjectOwnership(id),
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
