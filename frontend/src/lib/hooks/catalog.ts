import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createHelmRepository,
  deleteHelmRepository,
  getHelmCharts,
  getHelmChartVersions,
  getHelmRepositories,
  getInstalledCharts,
  installHelmChart,
  rollbackChart,
  syncHelmRepository,
  uninstallChart,
  upgradeInstalledChart,
  type InstallHelmChartRequest,
} from "@/lib/api/catalog";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { HelmRepoType } from "@/types";

export function useHelmRepositories() {
  return useQuery({
    queryKey: queryKeys.catalog.repositories,
    queryFn: getHelmRepositories,
  });
}

export function useCreateHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      url: string;
      repoType: HelmRepoType;
      description?: string;
      username?: string;
      password?: string;
    }) => createHelmRepository(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Repository added");
    },
    onError: (error: Error) => toastApiError("Failed to add repository", error),
  });
}

export function useSyncHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: syncHelmRepository,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Repository sync initiated");
    },
    onError: (error: Error) =>
      toastApiError("Failed to sync repository", error),
  });
}

export function useDeleteHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: deleteHelmRepository,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Repository deleted");
    },
    onError: (error: Error) =>
      toastApiError("Failed to delete repository", error),
  });
}

export type HelmChartQuery = Record<string, unknown> & {
  projectId: string;
  repository?: string;
  category?: string;
  search?: string;
};

export function useHelmCharts(params: HelmChartQuery) {
  return useQuery({
    queryKey: queryKeys.catalog.charts(params),
    queryFn: () => getHelmCharts(params),
    enabled: !!params.projectId,
  });
}

export function useHelmChartVersions(projectId: string, chartId: string) {
  return useQuery({
    queryKey: queryKeys.catalog.chartVersions(projectId, chartId),
    queryFn: () => getHelmChartVersions(projectId, chartId),
    enabled: !!projectId && !!chartId,
  });
}

export function useInstalledCharts(params?: { cluster?: string }) {
  return useQuery({
    queryKey: queryKeys.catalog.installed(params),
    queryFn: () => getInstalledCharts(params),
    refetchInterval: liveFallback(30_000),
  });
}

export function useInstallHelmChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: InstallHelmChartRequest) => installHelmChart(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Chart installation initiated");
    },
    onError: (error: Error) => toastApiError("Failed to install chart", error),
  });
}

export function useUpgradeInstalledChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: { chart_version_id: string; values_override?: string };
    }) => upgradeInstalledChart(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Chart upgrade initiated");
    },
    onError: (error: Error) => toastApiError("Failed to upgrade chart", error),
  });
}

export function useUninstallChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: uninstallChart,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Chart uninstall initiated");
    },
    onError: (error: Error) =>
      toastApiError("Failed to uninstall chart", error),
  });
}

export function useRollbackChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: { id: string; revision: number }) =>
      rollbackChart(id, revision),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess("Chart rollback initiated");
    },
    onError: (error: Error) => toastApiError("Failed to rollback chart", error),
  });
}
