import { useClusterScopeStore } from "@/lib/cluster-scope";
import { useClusterResourceCounts } from "@/lib/hooks/kubernetes-proxy";

// Hook to fetch resource counts for a cluster.
//
// Counts are fetched in one metadata-only request per expanded group. The
// server applies per-resource and namespace RBAC and never returns object
// bodies; collapsed groups create no member-cluster traffic.
const RESOURCE_COUNT_GROUPS = {
  Cluster: ["nodes", "namespaces"],
  Workloads: [
    "pods",
    "deployments",
    "daemonsets",
    "statefulsets",
    "jobs",
    "cronjobs",
  ],
  "Service Discovery": ["services", "ingresses", "hpa"],
  Storage: [
    "persistentvolumes",
    "persistentvolumeclaims",
    "storageclasses",
    "configmaps",
    "secrets",
  ],
  Policy: [
    "networkpolicies",
    "resourcequotas",
    "limitranges",
    "poddisruptionbudgets",
  ],
  RBAC: [
    "serviceaccounts",
    "k8s-clusterroles",
    "k8s-clusterrolebindings",
    "k8s-roles",
    "k8s-rolebindings",
  ],
  "More Resources": ["crds", "endpoints", "replicasets"],
} as const;

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
  const cluster = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Cluster,
    countNamespaces,
    scopeReady && openGroups.has("Cluster"),
  );
  const workloads = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Workloads,
    countNamespaces,
    scopeReady && openGroups.has("Workloads"),
  );
  const serviceDiscovery = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS["Service Discovery"],
    countNamespaces,
    scopeReady && openGroups.has("Service Discovery"),
  );
  const storage = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Storage,
    countNamespaces,
    scopeReady && openGroups.has("Storage"),
  );
  const policy = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.Policy,
    countNamespaces,
    scopeReady && openGroups.has("Policy"),
  );
  const rbacCounts = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS.RBAC,
    countNamespaces,
    scopeReady && openGroups.has("RBAC"),
  );
  const more = useClusterResourceCounts(
    clusterId,
    RESOURCE_COUNT_GROUPS["More Resources"],
    countNamespaces,
    scopeReady && openGroups.has("More Resources"),
  );

  const counts = Object.assign(
    {},
    cluster.data?.counts,
    workloads.data?.counts,
    serviceDiscovery.data?.counts,
    storage.data?.counts,
    policy.data?.counts,
    rbacCounts.data?.counts,
    more.data?.counts,
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
