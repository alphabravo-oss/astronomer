import { useQuery } from "@tanstack/react-query";
import {
  getCatalogApplicationPresentations,
  getHelmChart,
} from "@/lib/api/catalog";
import { getCatalogSupportRepository } from "@/lib/api/catalog-support";
import { queryKeys } from "@/lib/query-keys";
import { supportForChart } from "@/lib/catalogs/support";
import { OfferingSupport } from "./offering-support";

export function CatalogOfferingSupport({
  chart,
  repository,
  repositoryId,
  version,
  detail = false,
}: {
  chart: string;
  repository?: string;
  repositoryId?: string;
  version?: string;
  detail?: boolean;
}) {
  const catalog = useQuery({
    queryKey: queryKeys.catalog.applications,
    queryFn: ({ signal }) => getCatalogApplicationPresentations(signal),
    retry: false,
    throwOnError: false,
  });
  const repo = useQuery({
    queryKey: queryKeys.catalog.repository(repositoryId ?? ""),
    queryFn: ({ signal }) => getCatalogSupportRepository(repositoryId!, signal),
    enabled: !!repositoryId,
    retry: false,
    throwOnError: false,
  });
  const identity = repositoryId
    ? !repo.isError
      ? repo.data?.url
      : undefined
    : repository;
  return (
    <OfferingSupport
      detail={detail}
      support={supportForChart(
        catalog.isError ? undefined : catalog.data,
        chart,
        identity,
        version,
      )}
    />
  );
}

export function ApplicationOfferingSupport({
  projectId,
  chartId,
  version,
}: {
  projectId: string;
  chartId: string;
  version?: string;
}) {
  const chart = useQuery({
    queryKey: queryKeys.catalog.charts({ projectId, chartId }),
    queryFn: ({ signal }) =>
      getHelmChart(projectId, chartId, "project", signal),
    enabled: !!projectId && !!chartId,
    retry: false,
    throwOnError: false,
  });
  return (
    <CatalogOfferingSupport
      chart={chart.isError ? "" : (chart.data?.name ?? "")}
      repositoryId={chart.isError ? undefined : chart.data?.repositoryId}
      version={version}
      detail
    />
  );
}
