import {
  countClusterResources,
  listGenericClusterResources,
  searchResourcesAcrossClusters,
} from "@/lib/api/generated/client";
import { mapPage } from "@/lib/api/pagination";
import type { GenericK8sResource, PaginatedResponse } from "@/types";
import type { ResourceCounts } from "@/types/openapi.generated";

export async function getGenericResources(
  clusterId: string,
  resourceType: string,
  params?: {
    namespace?: string;
    namespaces?: string;
    limit?: number;
    offset?: number;
    search?: string;
    sort?: string;
    signal?: AbortSignal;
  },
): Promise<PaginatedResponse<GenericK8sResource>> {
  const response = await listGenericClusterResources({
    path: { cluster_id: clusterId, resource_type: resourceType },
    query: {
      namespace: params?.namespace,
      namespaces: params?.namespaces,
      limit: params?.limit,
      offset: params?.offset,
      search: params?.search,
      sort: params?.sort,
    },
    signal: params?.signal,
  });
  return mapPage(
    {
      data: (response.data ?? []) as GenericK8sResource[],
      pagination: response.pagination,
    },
    (row) => row,
  );
}

export async function getClusterResourceCounts(
  clusterId: string,
  resourceTypes: readonly string[],
  namespaces: readonly string[] | null,
  signal?: AbortSignal,
): Promise<ResourceCounts> {
  const response = await countClusterResources({
    path: { cluster_id: clusterId },
    query: {
      resources: [...resourceTypes].sort().join(","),
      ...(namespaces && namespaces.length > 0
        ? { namespace: [...namespaces].sort().join(",") }
        : {}),
    },
    signal,
  });
  return response.data ?? { counts: {} };
}

// Matches searchResourceDefs in internal/handler/resources_search.go.
export type SearchableResourceType =
  | "pods"
  | "services"
  | "configmaps"
  | "secrets"
  | "namespaces"
  | "nodes"
  | "persistentvolumeclaims"
  | "deployments"
  | "statefulsets"
  | "daemonsets"
  | "jobs"
  | "cronjobs"
  | "ingresses";

export interface SearchResourcesParams {
  type: SearchableResourceType;
  namespace?: string;
  label?: string;
  field?: string;
  name?: string;
  limit?: number;
}

export interface SearchResultRow extends Record<string, unknown> {
  cluster_id: string;
  cluster_name: string;
  clusterId: string;
  clusterName: string;
  name?: string;
  namespace?: string;
  status?: string;
  type?: string;
  age?: string;
}

export interface SearchClusterError {
  cluster_id: string;
  cluster_name: string;
  error: string;
}

export interface SearchResourcesResponse {
  results: SearchResultRow[];
  errors: SearchClusterError[];
  clusters_queried?: number;
  clusters_failed?: number;
  clustersQueried: number;
  clustersFailed: number;
  type: string;
}

export async function searchResources(
  params: SearchResourcesParams,
  signal?: AbortSignal,
): Promise<SearchResourcesResponse> {
  const response = await searchResourcesAcrossClusters({
    query: params,
    signal,
  });
  const wire = response.data;
  return {
    results: (wire?.results ?? []).map((row) => ({
      ...row,
      cluster_id: row.cluster_id ?? "",
      cluster_name: row.cluster_name ?? "",
      clusterId: row.cluster_id ?? "",
      clusterName: row.cluster_name ?? row.clusterName ?? "",
    })),
    errors: wire?.errors ?? [],
    clusters_queried: wire?.clusters_queried,
    clusters_failed: wire?.clusters_failed,
    clustersQueried: wire?.clusters_queried ?? 0,
    clustersFailed: wire?.clusters_failed ?? 0,
    type: wire?.type ?? params.type,
  };
}
