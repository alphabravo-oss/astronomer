import { queryKeys } from "./query-keys";

describe("delivery query keys", () => {
  it("isolates projects, filters, details, and cluster inventory", () => {
    expect(queryKeys.delivery.sources("project-a", { limit: 25 })).toEqual([
      "delivery",
      "project-a",
      "sources",
      { limit: 25 },
    ]);
    expect(queryKeys.delivery.rollout("project-a", "rollout-1")).toEqual([
      "delivery",
      "project-a",
      "rollouts",
      "detail",
      "rollout-1",
    ]);
    expect(
      queryKeys.delivery.clusterInventory("project-b", "cluster-1"),
    ).toEqual(["delivery", "project-b", "clusters", "cluster-1", "inventory"]);
  });

  it("keeps cluster-agent caches separate from delivery caches", () => {
    expect(queryKeys.agents.all).toEqual(["cluster-agents"]);
    expect(queryKeys.agents.operations("cluster-1")).toEqual([
      "cluster-agents",
      "cluster-1",
      "operations",
    ]);
    expect(queryKeys.delivery.all).toEqual(["delivery"]);
  });
});

describe("observability query keys", () => {
  it("scopes alert rules and logging pipelines per cluster", () => {
    expect(queryKeys.alerting.rules()).toEqual(["alerting", "rules", "all"]);
    expect(queryKeys.alerting.rules("cluster-1")).toEqual([
      "alerting",
      "rules",
      "cluster-1",
    ]);
    expect(queryKeys.alerting.rulesAll).toEqual(["alerting", "rules"]);
    expect(queryKeys.logging.pipelines()).toEqual(["logging", "pipelines", "all"]);
    expect(queryKeys.logging.pipelines("cluster-1")).toEqual([
      "logging",
      "pipelines",
      "cluster-1",
    ]);
    expect(queryKeys.logging.pipelinesAll).toEqual(["logging", "pipelines"]);
  });
});
