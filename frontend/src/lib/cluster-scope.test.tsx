import {
  projectInCluster,
  projectSelectionSearch,
} from "./cluster-scope-collection";
import { describe, it, expect } from "vitest";
it("accepts secondary cluster membership", () => {
  expect(
    projectInCluster({ clusterId: "a", clusterIds: ["a", "b"] }, "b"),
  ).toBe(true);
  expect(projectInCluster({ clusterId: "a" }, "b")).toBe(false);
});
describe("project scope transaction", () => {
  it("replaces previous namespaces and dependent pagination atomically", () => {
    const result = projectSelectionSearch(
      new URLSearchParams(
        "project=a&namespaces=old&page=3&selected=x&install=chart",
      ),
      "b",
      ["team-b"],
    );
    expect(result.toString()).toBe("project=b&namespaces=team-b");
  });
  it("fails closed while resolving a project", () => {
    expect(
      projectSelectionSearch(new URLSearchParams(), "b").get("namespaces"),
    ).toBe("");
  });
});
