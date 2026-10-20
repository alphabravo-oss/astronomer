import { useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { AppInstallPage } from "@/components/clusters/app-install-page";
import { ConfigurationPage } from "@/components/catalog/configuration-page";
import {
  CatalogProjectPicker,
  useCatalogProjectScope,
} from "@/components/catalog/project-scope";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { QueryStates } from "@/components/ui/query-states";
import { getHelmChart } from "@/lib/api/catalog";
import { queryKeys } from "@/lib/query-keys";
import { usePermissionDecision } from "@/lib/permission-hooks";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type ApplicationConfigurationSearch = {
  chart?: string;
  version?: string;
  project?: string;
  cluster?: string;
  release?: string;
  origin?: string;
};
export function ApplicationConfiguration({
  search,
  release,
}: {
  search: ApplicationConfigurationSearch;
  release?: OpenAPIComponents["schemas"]["InstalledAppEnriched"];
}) {
  const navigate = useNavigate();
  const clusterId = release?.cluster_id ?? search.cluster ?? "";
  const chartId = release?.chart_id ?? search.chart ?? "";
  const scope = useCatalogProjectScope(search.project ?? "", clusterId);
  const projectId = scope.projectId;
  const chart = useQuery({
    queryKey: queryKeys.catalog.chart(projectId, chartId),
    queryFn: ({ signal }) =>
      getHelmChart(projectId, chartId, "project", signal),
    enabled: !!projectId && !!chartId,
    throwOnError: false,
  });
  const decision = usePermissionDecision(
    "catalog",
    release ? "update" : "create",
    release
      ? { type: "cluster", id: clusterId }
      : { type: "project", id: projectId },
  );
  const allowedClusters = [
    scope.project?.clusterId,
    ...(scope.project?.clusterIds ?? []),
  ].filter((id): id is string => !!id);
  const back = () =>
    void navigate({
      to:
        search.origin === "cluster" && clusterId
          ? `/dashboard/clusters/${clusterId}/apps?project=${encodeURIComponent(projectId)}&section=installed`
          : `/dashboard/catalog?project=${encodeURIComponent(projectId)}`,
    });
  const change = (patch: Partial<ApplicationConfigurationSearch>) =>
    void navigate({
      to: "/dashboard/catalog/configure",
      search: { ...search, ...patch },
    });
  if (release?.source_kind === "tool")
    return (
      <ConfigurationPage title="Managed by Tools" onClose={back}>
        <p>Configure this release from its Tools page.</p>
      </ConfigurationPage>
    );
  if (!chartId)
    return (
      <ConfigurationPage title="Application unavailable" onClose={back}>
        <p role="alert">Choose an application from the catalog.</p>
      </ConfigurationPage>
    );
  if (!projectId || !clusterId || !allowedClusters.includes(clusterId))
    return (
      <ConfigurationPage
        title="Install application"
        subtitle="Choose the project visibility and destination cluster."
        onClose={back}
      >
        <CatalogProjectPicker
          scope={scope}
          value={search.project ?? ""}
          onChange={(project) => change({ project })}
          clusterId={clusterId || undefined}
        />
        {projectId && (
          <div className="max-w-xl space-y-2">
            <label
              htmlFor="catalog-target-cluster"
              className="text-sm font-medium"
            >
              Target cluster
            </label>
            <RemoteClusterPicker
              id="catalog-target-cluster"
              ariaLabel="Target cluster"
              value={clusterId}
              allowedClusterIds={allowedClusters}
              onChange={(cluster) => change({ cluster })}
            />
          </div>
        )}
      </ConfigurationPage>
    );
  return (
    <QueryStates
      query={chart}
      permission="catalog:read"
      errorTitle="Application unavailable"
    >
      {(resolved) => (
        <AppInstallPage
          key={`${projectId}:${clusterId}:${chartId}:${release?.id ?? "install"}`}
          projectId={projectId}
          clusterId={clusterId}
          submitDecision={decision}
          onClose={back}
          onOperationStarted={(id) =>
            void navigate({
              to: `/dashboard/clusters/${clusterId}/apps?project=${encodeURIComponent(projectId)}&section=installed&operation=${encodeURIComponent(id)}`,
            })
          }
          mode={
            release
              ? {
                  kind: "upgrade",
                  installedChartId: release.id!,
                  chartId,
                  chartName: resolved.name,
                  currentVersionId: release.chart_version_id!,
                  currentValues: "",
                  releaseName: release.release_name!,
                  namespace: release.namespace!,
                }
              : {
                  kind: "install",
                  chartId,
                  chartName: resolved.name,
                  versionId: search.version,
                }
          }
        />
      )}
    </QueryStates>
  );
}
