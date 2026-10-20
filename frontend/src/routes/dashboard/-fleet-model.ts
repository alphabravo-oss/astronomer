import type { Cluster } from "@/types";

/** Largest page the clusters API serves; the overview reads one page. */
export const FLEET_PAGE_SIZE = 200;

export type FleetHealth = "healthy" | "attention" | "disconnected" | "registering";

export interface FleetModel {
  /** Total registered clusters (the list's pagination total). */
  total: number;
  /** Clusters loaded into the overview (≤ FLEET_PAGE_SIZE). */
  clusters: Cluster[];
  /** True when `total` exceeds the loaded page. */
  truncated: boolean;
  byHealth: Record<FleetHealth, Cluster[]>;
  counts: Record<FleetHealth, number>;
  nodes: number;
  pods: number;
}

/**
 * Agent connectivity and cluster health are the same fact seen twice: every
 * cluster has exactly one agent, and the cluster status is derived from that
 * agent's session. Classifying once here is what keeps the verdict strip,
 * the health board, the agents panel and the capacity list in agreement.
 */
export function clusterHealth(cluster: Pick<Cluster, "status">): FleetHealth {
  switch (cluster.status) {
    case "active":
      return "healthy";
    case "disconnected":
      return "disconnected";
    case "pending":
      return "registering";
    default:
      // error, unsupported, anything new the API adds
      return "attention";
  }
}

export function fleetModel(
  clusters: readonly Cluster[] | undefined,
  total: number | undefined,
): FleetModel {
  const list = [...(clusters ?? [])];
  const byHealth: Record<FleetHealth, Cluster[]> = {
    healthy: [],
    attention: [],
    disconnected: [],
    registering: [],
  };
  let nodes = 0;
  let pods = 0;
  for (const cluster of list) {
    byHealth[clusterHealth(cluster)].push(cluster);
    nodes += cluster.nodeCount ?? 0;
    pods += cluster.podCount ?? 0;
  }
  const resolvedTotal = Math.max(total ?? list.length, list.length);
  return {
    total: resolvedTotal,
    clusters: list,
    truncated: resolvedTotal > list.length,
    byHealth,
    counts: {
      healthy: byHealth.healthy.length,
      attention: byHealth.attention.length,
      disconnected: byHealth.disconnected.length,
      registering: byHealth.registering.length,
    },
    nodes,
    pods,
  };
}
