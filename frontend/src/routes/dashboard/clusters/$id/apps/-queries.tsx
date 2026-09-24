import { useQuery } from "@tanstack/react-query";
import { useDebouncedValue } from "@tanstack/react-pacer";
import {
  OffsetPagination,
  useOffsetPagination,
} from "@/components/ui/offset-pagination";
import { useHelmRepositories } from "@/lib/hooks/catalog";
import {
  listClusterApps,
  listCatalogCharts,
  listRecommendedCharts,
} from "@/lib/api/cluster-apps";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";
import type { Section } from "./-modal-state";

export function useAppsQueries(
  clusterId: string,
  projectId: string,
  section: Section,
  search: string,
) {
  const installedPaging = useOffsetPagination(clusterId);
  const browsePaging = useOffsetPagination(projectId);
  const reposPaging = useOffsetPagination("repositories");
  const [term] = useDebouncedValue(search, { wait: 200 });
  const installed = useQuery({
    queryKey: queryKeys.clusterPages.appsInstalled(
      clusterId,
      installedPaging.params,
    ),
    queryFn: ({ signal }) =>
      listClusterApps(clusterId, installedPaging.params, signal),
    throwOnError: false,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });
  const browse = useQuery({
    queryKey: queryKeys.clusterPages.appCatalogBrowse(
      projectId,
      term,
      browsePaging.params,
    ),
    queryFn: ({ signal }) =>
      listCatalogCharts({
        projectId,
        ...browsePaging.params,
        search: term,
        signal,
      }),
    enabled: section === "browse" && !!projectId,
    throwOnError: false,
  });
  const recommended = useQuery({
    queryKey: queryKeys.clusterPages.appCatalogRecommended(projectId),
    queryFn: ({ signal }) => listRecommendedCharts(projectId, 12, signal),
    enabled: section === "recommended" && !!projectId,
    throwOnError: false,
  });
  const reposQuery = useHelmRepositories(undefined, reposPaging.params);
  return {
    installed,
    browse,
    recommended,
    reposQuery,
    installedPaging,
    browsePaging,
    reposPaging,
  };
}
export function AppsPagination({
  section,
  queries,
}: {
  section: Section;
  queries: ReturnType<typeof useAppsQueries>;
}) {
  if (section === "installed")
    return (
      <OffsetPagination
        control={queries.installedPaging}
        query={queries.installed}
        label="installed apps"
      />
    );
  if (section === "browse")
    return (
      <OffsetPagination
        control={queries.browsePaging}
        query={queries.browse}
        label="charts"
      />
    );
  if (section === "repositories")
    return (
      <OffsetPagination
        control={queries.reposPaging}
        query={queries.reposQuery}
        label="repositories"
      />
    );
  return null;
}
