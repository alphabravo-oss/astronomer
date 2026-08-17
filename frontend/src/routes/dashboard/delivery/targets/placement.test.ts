import {
  placementFormDefaults,
  placementFromForm,
} from "@/components/delivery/target-form";

function form(values: Record<string, string>): FormData {
  const data = new FormData();
  for (const [key, value] of Object.entries(values)) data.set(key, value);
  return data;
}

describe("delivery target placement form", () => {
  it("keeps all-cluster selection explicit, ignores selectors, and preserves exclusions", () => {
    expect(
      placementFromForm(
        form({ cluster_ids: "cluster-1", exclude_ids: "cluster-z" }),
        true,
      ),
    ).toEqual({
      all_clusters: true,
      exclude_cluster_ids: ["cluster-z"],
    });
  });

  it("round-trips an existing placement into editable field values", () => {
    expect(
      placementFormDefaults({
        allClusters: false,
        clusterIds: ["cluster-1", "cluster-2"],
        clusterGroupIds: ["group-a"],
        matchLabels: { environment: "production" },
        matchExpressions: [
          { key: "region", operator: "In", values: ["east", "west"] },
          { key: "gpu", operator: "DoesNotExist" },
        ],
        excludeClusterIds: ["cluster-2"],
      }),
    ).toEqual({
      allClusters: false,
      clusterIds: "cluster-1, cluster-2",
      groupIds: "group-a",
      labels: "environment=production",
      expressions: "region In east,west\ngpu DoesNotExist",
      excludeIds: "cluster-2",
    });
  });

  it("serializes explicit, group, label, expression, and exclusion selectors", () => {
    expect(
      placementFromForm(
        form({
          cluster_ids: "cluster-1, cluster-2",
          group_ids: "group-a",
          labels: "environment=production\nregion=us-east",
          expressions: "tier In platform,edge\nmaintenance DoesNotExist",
          exclude_ids: "cluster-2",
        }),
        false,
      ),
    ).toEqual({
      all_clusters: false,
      cluster_ids: ["cluster-1", "cluster-2"],
      cluster_group_ids: ["group-a"],
      match_labels: { environment: "production", region: "us-east" },
      match_expressions: [
        { key: "tier", operator: "In", values: ["platform", "edge"] },
        { key: "maintenance", operator: "DoesNotExist", values: undefined },
      ],
      exclude_cluster_ids: ["cluster-2"],
    });
  });

  it("rejects malformed labels and expressions instead of guessing", () => {
    expect(() =>
      placementFromForm(form({ labels: "production" }), false),
    ).toThrow("expected key=value");
    expect(() =>
      placementFromForm(form({ expressions: "tier Around platform" }), false),
    ).toThrow("Invalid expression");
    expect(() =>
      placementFromForm(form({ expressions: "tier In" }), false),
    ).toThrow("requires values");
    expect(() =>
      placementFromForm(form({ expressions: "gpu Exists true" }), false),
    ).toThrow("cannot include values");
  });
});
