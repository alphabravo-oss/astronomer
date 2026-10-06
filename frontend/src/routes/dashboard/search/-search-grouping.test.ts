import { groupResultsByCluster } from "./-search-grouping";
import type { SearchResultRow } from "@/lib/api/resource-search";

function row(
  clusterId: string,
  clusterName: string,
  name: string,
  status?: string,
): SearchResultRow {
  return {
    cluster_id: clusterId,
    cluster_name: clusterName,
    clusterId,
    clusterName,
    name,
    status,
  };
}

describe("groupResultsByCluster", () => {
  it("groups rows per cluster with status counts, sorted by name", () => {
    const groups = groupResultsByCluster([
      row("b", "zeta", "api-1", "Running"),
      row("a", "alpha", "api-2", "Running"),
      row("a", "alpha", "api-3", "Pending"),
      row("a", "alpha", "api-4"),
    ]);
    expect(groups.map((g) => g.clusterName)).toEqual(["alpha", "zeta"]);
    expect(groups[0]).toMatchObject({
      total: 3,
      statusCounts: { Running: 1, Pending: 1, Unknown: 1 },
    });
    expect(groups[1].total).toBe(1);
  });

  it("includes clusters that failed to search with their error", () => {
    const groups = groupResultsByCluster(
      [row("a", "alpha", "api-1", "Running")],
      [{ cluster_id: "c", cluster_name: "gamma", error: "tunnel timeout" }],
    );
    expect(groups.map((g) => g.clusterName)).toEqual(["alpha", "gamma"]);
    expect(groups[1]).toMatchObject({ total: 0, error: "tunnel timeout" });
    expect(groups[0].error).toBeUndefined();
  });
});
