import { useQuery } from "@tanstack/react-query";
import { getCRDNavCounts } from "@/lib/api/crd-counts";
import { useClusterScopeStore } from "@/lib/cluster-scope";
import { queryKeys } from "@/lib/query-keys";
import type { ClusterDiscovery } from "./cluster-discovery-model";
import type { NavGroup } from "./sidebar-navigation";
import { navGroupItems } from "./nav-group-items";

export function useCRDNavCounts(
  clusterId: string,
  groups: NavGroup[],
  discovery: ClusterDiscovery,
  expanded: boolean,
) {
  const namespaces = useClusterScopeStore(
    (state) => state.namespacesByCluster[clusterId],
  );
  const visible = new Set(
    groups
      .filter((group) => group.label === "More Resources")
      .flatMap(navGroupItems)
      .map((item) => item.countKey),
  );
  const types = [...discovery.crdsByGroup.values()]
    .flat()
    .filter((type) => visible.has(`crd:${type.group}/${type.plural}`))
    .sort((a, b) =>
      `${a.group}/${a.plural}`.localeCompare(`${b.group}/${b.plural}`),
    )
    .slice(0, 15);
  const query = useQuery({
    queryKey: queryKeys.generic.resourceCounts(
      clusterId,
      types.map((type) => `crd:${type.group}/${type.version}/${type.plural}`),
      namespaces === undefined ? [] : namespaces,
    ),
    queryFn: ({ signal }) =>
      getCRDNavCounts(clusterId, types, namespaces ?? null, signal),
    enabled:
      !!clusterId &&
      expanded &&
      !discovery.isError &&
      namespaces !== undefined &&
      namespaces?.length !== 0 &&
      types.length > 0,
    staleTime: 30_000,
    retry: false,
    throwOnError: false,
  });
  return expanded &&
    !discovery.isError &&
    namespaces !== undefined &&
    namespaces?.length !== 0
    ? (query.data ?? {})
    : {};
}
