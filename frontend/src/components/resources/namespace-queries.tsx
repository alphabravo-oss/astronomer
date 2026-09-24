import { useQuery, type QueryKey } from "@tanstack/react-query";
import {
  OffsetPagination,
  useOffsetPagination,
} from "@/components/ui/offset-pagination";
import { QueryStates } from "@/components/ui/query-states";
import {
  getClusterEvents,
  getClusterNamespaces,
  getClusterPods,
  getWorkloads,
} from "@/lib/api/workloads";
import { getGenericResources } from "@/lib/api/resource-search";
import {
  getNamedResources,
  type NamedResourceType,
} from "@/lib/api/kubernetes-resources";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";
import type {
  Ingress,
  K8sService,
  NetworkPolicy,
  PersistentVolumeClaim,
  PaginatedResponse,
} from "@/types";

type Params = { namespace: string; limit: number; offset: number };
function useNamespacePage<T>(
  scope: string,
  namespace: string,
  label: string,
  key: (params: Params) => QueryKey,
  fetchPage: (
    params: Params,
    signal: AbortSignal,
  ) => Promise<PaginatedResponse<T>>,
) {
  const control = useOffsetPagination(`${scope}:${namespace}:${label}`);
  const params = { namespace, ...control.params };
  const query = useQuery({
    queryKey: key(params),
    queryFn: ({ signal }) => fetchPage(params, signal),
    enabled: !!scope && !!namespace,
    throwOnError: false,
    refetchInterval: (query) =>
      query.state.status === "error" ? false : liveFallback(30_000)(),
  });
  return {
    isError: query.isError,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    error: query.error,
    refetch: query.refetch,
    data: query.isError ? undefined : query.data,
    control,
    label,
  };
}
function useNamedPage<T>(
  clusterId: string,
  namespace: string,
  type: NamedResourceType,
) {
  return useNamespacePage<T>(
    clusterId,
    namespace,
    type,
    (params) => queryKeys.generic.namedResources(clusterId, type, params),
    (params, signal) =>
      getNamedResources<T>(clusterId, type, { ...params, signal }),
  );
}
function useGenericPage(clusterId: string, namespace: string, type: string) {
  return useNamespacePage(
    clusterId,
    namespace,
    type,
    (params) => queryKeys.generic.resources(clusterId, type, params),
    (params, signal) =>
      getGenericResources(clusterId, type, { ...params, signal }),
  );
}
export function useNamespaceQueries(clusterId: string, namespace: string) {
  const namespaces = useQuery({
    queryKey: queryKeys.clusters.namespaces(clusterId),
    queryFn: ({ signal }) => getClusterNamespaces(clusterId, signal),
    enabled: !!clusterId,
    throwOnError: false,
  });
  const events = useQuery({
    queryKey: queryKeys.clusters.events(clusterId, { limit: 500 }),
    queryFn: ({ signal }) =>
      getClusterEvents(clusterId, { limit: 500, signal }),
    enabled: !!clusterId,
    throwOnError: false,
    refetchInterval: (query) =>
      query.state.status === "error" ? false : liveFallback(15_000)(),
  });
  const workloads = useNamespacePage(
    clusterId,
    namespace,
    "workloads",
    (params) =>
      queryKeys.workloads.list(clusterId, {
        namespace,
        offset: params.offset,
        pageSize: params.limit,
      }),
    (params, signal) =>
      getWorkloads(clusterId, {
        namespace,
        offset: params.offset,
        pageSize: params.limit,
        signal,
      }),
  );
  const pods = useNamespacePage(
    clusterId,
    namespace,
    "pods",
    (params) => queryKeys.clusters.pods(clusterId, params),
    (params, signal) => getClusterPods(clusterId, { ...params, signal }),
  );
  const services = useNamedPage<K8sService>(clusterId, namespace, "services");
  const ingresses = useNamedPage<Ingress>(clusterId, namespace, "ingresses");
  const policies = useNamedPage<NetworkPolicy>(
    clusterId,
    namespace,
    "networkpolicies",
  );
  const pvcs = useNamedPage<PersistentVolumeClaim>(
    clusterId,
    namespace,
    "persistentvolumeclaims",
  );
  const configMaps = useGenericPage(clusterId, namespace, "configmaps");
  const secrets = useGenericPage(clusterId, namespace, "secrets");
  const quotas = useGenericPage(clusterId, namespace, "resourcequotas");
  const limits = useGenericPage(clusterId, namespace, "limitranges");
  const serviceAccounts = useGenericPage(
    clusterId,
    namespace,
    "serviceaccounts",
  );
  const roles = useGenericPage(clusterId, namespace, "k8s-roles");
  const roleBindings = useGenericPage(clusterId, namespace, "k8s-rolebindings");
  const groups = {
    workloads: [workloads],
    pods: [pods],
    networking: [services, ingresses, policies],
    storage: [pvcs],
    configuration: [configMaps, secrets, quotas, limits],
    security: [serviceAccounts, roles, roleBindings, policies],
  };
  return {
    namespaces,
    events,
    workloads,
    pods,
    services,
    ingresses,
    policies,
    pvcs,
    configMaps,
    secrets,
    quotas,
    limits,
    serviceAccounts,
    roles,
    roleBindings,
    groups,
  };
}

export function NamespacePageControls({
  queries,
  tab,
}: {
  queries: ReturnType<typeof useNamespaceQueries>;
  tab: string;
}) {
  const pages =
    tab in queries.groups
      ? queries.groups[tab as keyof typeof queries.groups]
      : [];
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        Resource counts and health indicators describe loaded pages, not
        namespace totals. Each resource type pages independently. Events are
        this namespace's matches within the latest 500 cluster events, not
        complete history.
      </p>
      {(tab === "events" || tab === "overview") && queries.events.isError && (
        <QueryStates query={queries.events}>{null}</QueryStates>
      )}
      {tab === "overview" &&
        Object.values(queries.groups)
          .flat()
          .filter(
            (query, index, all) =>
              query.isError &&
              all.findIndex((other) => other.label === query.label) === index,
          )
          .map((query) => (
            <div key={query.label}>
              <span>{query.label} unavailable</span>
              <QueryStates<unknown> query={query}>{null}</QueryStates>
            </div>
          ))}
      {pages.map((query) => (
        <div key={query.label} className="space-y-2">
          <span className="text-sm font-medium">{query.label}</span>
          {(query.isError || query.isLoading) && (
            <QueryStates<unknown> query={query}>{null}</QueryStates>
          )}
          <OffsetPagination
            control={query.control}
            query={query}
            label={query.label}
          />
        </div>
      ))}
    </div>
  );
}
