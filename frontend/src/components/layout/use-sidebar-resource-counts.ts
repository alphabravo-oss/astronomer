import { useClusterScopeStore } from "@/lib/cluster-scope";
import { useClusterResourceCounts } from "@/lib/hooks/kubernetes-proxy";

/**
 * Inventory counts for a cluster, one metadata-only request per group. The
 * server applies per-resource and namespace RBAC and never returns object
 * bodies. Counts are shown on the cluster overview (group headers), never in
 * the sidebar — the same hook feeds both the KPI tiles and the inventory so
 * the two can never disagree.
 */
export const RESOURCE_COUNT_GROUPS = {
  Overview: ["nodes", "namespaces"],
  Workloads: [
    "pods",
    "deployments",
    "daemonsets",
    "statefulsets",
    "jobs",
    "cronjobs",
    "hpa",
  ],
  Networking: ["services", "ingresses", "networkpolicies"],
  Storage: [
    "persistentvolumes",
    "persistentvolumeclaims",
    "storageclasses",
    "configmaps",
    "secrets",
  ],
  "Policy & access": [
    "resourcequotas",
    "limitranges",
    "poddisruptionbudgets",
    "serviceaccounts",
    "k8s-clusterroles",
    "k8s-clusterrolebindings",
    "k8s-roles",
    "k8s-rolebindings",
  ],
  Manage: ["crds", "endpoints", "replicasets"],
} as const;

export type ResourceCountGroup = keyof typeof RESOURCE_COUNT_GROUPS;

export function useSidebarResourceCounts(
  clusterId: string,
  openGroups: Set<string>,
) {
  const selectedNamespaces = useClusterScopeStore(
    (state) => state.namespacesByCluster[clusterId],
  );
  const countNamespaces =
    selectedNamespaces === undefined ? [] : selectedNamespaces;
  const scopeReady = selectedNamespaces !== undefined;
  const overview = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Overview,
    countNamespaces,
    scopeReady && openGroups.has("Overview"),
  );
  const workloads = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Workloads,
    countNamespaces,
    scopeReady && openGroups.has("Workloads"),
  );
  const networking = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Networking,
    countNamespaces,
    scopeReady && openGroups.has("Networking"),
  );
  const storage = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Storage,
    countNamespaces,
    scopeReady && openGroups.has("Storage"),
  );
  const policy = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS["Policy & access"],
    countNamespaces,
    scopeReady && openGroups.has("Policy & access"),
  );
  const manage = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Manage,
    countNamespaces,
    scopeReady && openGroups.has("Manage"),
  );

  const counts = Object.assign(
    {},
    overview.data?.counts,
    workloads.data?.counts,
    networking.data?.counts,
    storage.data?.counts,
    policy.data?.counts,
    manage.data?.counts,
  );
  return {
    ...counts,
    pvs: counts.persistentvolumes,
    pvcs: counts.persistentvolumeclaims,
    k8sClusterroles: counts["k8s-clusterroles"],
    k8sClusterrolebindings: counts["k8s-clusterrolebindings"],
    k8sRoles: counts["k8s-roles"],
    k8sRolebindings: counts["k8s-rolebindings"],
  };
}

const ALL_GROUPS = new Set<string>(Object.keys(RESOURCE_COUNT_GROUPS));

/** Every inventory group at once (cluster overview). */
export function useClusterInventoryCounts(clusterId: string) {
  return useSidebarResourceCounts(clusterId, ALL_GROUPS);
}
