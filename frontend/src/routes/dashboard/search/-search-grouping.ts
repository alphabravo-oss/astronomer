import type {
  SearchClusterError,
  SearchResultRow,
} from "@/lib/api/resource-search";

export interface ClusterResultGroup {
  clusterId: string;
  clusterName: string;
  total: number;
  /** Row count per status label ("Running", "Degraded", ...), unknown -> "Unknown". */
  statusCounts: Record<string, number>;
  /** Set when the fan-out could not search this cluster. */
  error?: string;
}

/**
 * Collapse the flat fan-out result into one summary per cluster. Clusters
 * that failed (and so contributed no rows) are included with their error so
 * the operator can tell "no match" from "not searched". Sorted by cluster
 * name for a stable layout.
 */
export function groupResultsByCluster(
  rows: SearchResultRow[],
  errors: SearchClusterError[] = [],
): ClusterResultGroup[] {
  const groups = new Map<string, ClusterResultGroup>();
  for (const row of rows) {
    const id = row.clusterId || row.cluster_id;
    let group = groups.get(id);
    if (!group) {
      group = {
        clusterId: id,
        clusterName: row.clusterName || row.cluster_name || id,
        total: 0,
        statusCounts: {},
      };
      groups.set(id, group);
    }
    const status = row.status ? String(row.status) : "Unknown";
    group.total += 1;
    group.statusCounts[status] = (group.statusCounts[status] ?? 0) + 1;
  }
  for (const err of errors) {
    const existing = groups.get(err.cluster_id);
    if (existing) {
      existing.error = err.error;
    } else {
      groups.set(err.cluster_id, {
        clusterId: err.cluster_id,
        clusterName: err.cluster_name || err.cluster_id,
        total: 0,
        statusCounts: {},
        error: err.error,
      });
    }
  }
  return [...groups.values()].sort((a, b) =>
    a.clusterName.localeCompare(b.clusterName),
  );
}
