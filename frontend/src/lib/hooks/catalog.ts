import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createHelmRepository,
  deleteHelmRepository,
  getApplicationCatalogSources,
  getCatalogApplicationPresentations,
  getCatalogUserDiscovery,
  getHelmCharts,
  getHelmChartVersions,
  getHelmRepositories,
  getInstalledChartUpgradeVersions,
  getInstalledCharts,
  installHelmChart,
  previewCatalogInstallation,
  rollbackChart,
  setCatalogChartFavorite,
  syncHelmRepository,
  uninstallChart,
  upgradeInstalledChart,
  type InstallHelmChartRequest,
} from "@/lib/api/catalog";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { HelmRepoType } from "@/types";

export function useHelmRepositories(clusterId?: string) {
  return useQuery({
    queryKey: queryKeys.catalog.repositoriesFor(clusterId),
    queryFn: ({ signal }) => getHelmRepositories(clusterId, signal),
  });
}

export function useCatalogApplications() {
  return useQuery({
    queryKey: queryKeys.catalog.applications,
    queryFn: ({ signal }) => getCatalogApplicationPresentations(signal),
  });
}

export function useApplicationCatalogSources() {
  return useQuery({
    queryKey: queryKeys.catalog.applicationSources,
    queryFn: ({ signal }) => getApplicationCatalogSources(signal),
  });
}

export function useCatalogUserDiscovery() {
  return useQuery({
    queryKey: queryKeys.catalog.discovery,
    queryFn: ({ signal }) => getCatalogUserDiscovery(signal),
  });
}

export function useSetCatalogChartFavorite() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      chartId,
      favorite,
      clusterId,
      projectId,
    }: {
      chartId: string;
      favorite: boolean;
      clusterId?: string;
      projectId?: string;
    }) => setCatalogChartFavorite(chartId, favorite, { clusterId, projectId }),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.discovery }),
    onError: (error: Error) =>
      toastApiError("Failed to update favorite", error),
  });
}

export function usePreviewCatalogInstallation() {
  return useMutation({
    mutationFn: previewCatalogInstallation,
    onError: (error: Error) =>
      toastApiError("Failed to preview installation", error),
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
  clusterId?: string;
  projectId?: string;
  repository?: string;
  category?: string;
  search?: string;
};

export function useHelmCharts(params: HelmChartQuery) {
  return useQuery({
    queryKey: queryKeys.catalog.charts(params),
    queryFn: ({ signal }) => getHelmCharts(params, signal),
    enabled: !!params.clusterId || !!params.projectId,
  });
}

export function useHelmChartVersions(
  scopeId: string,
  chartId: string,
  scope: "cluster" | "project" = "project",
) {
  return useQuery({
    queryKey: queryKeys.catalog.chartVersions(scopeId, chartId, scope),
    queryFn: ({ signal }) =>
      getHelmChartVersions(scopeId, chartId, scope, signal),
    enabled: !!scopeId && !!chartId,
  });
}

export function useInstalledChartUpgradeVersions(installationId: string) {
  return useQuery({
    queryKey: queryKeys.catalog.upgradeVersions(installationId),
    queryFn: ({ signal }) =>
      getInstalledChartUpgradeVersions(installationId, signal),
    enabled: Boolean(installationId),
  });
}

export function useInstalledCharts(params?: { cluster?: string }) {
  return useQuery({
    queryKey: queryKeys.catalog.installed(params),
    queryFn: ({ signal }) => getInstalledCharts(params, signal),
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
