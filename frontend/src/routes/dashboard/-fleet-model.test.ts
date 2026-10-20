import { describe, expect, it } from "vitest";
import type { Cluster } from "@/types";
import { clusterHealth, fleetModel } from "./-fleet-model";

const cluster = (id: string, status: Cluster["status"], pods = 0, nodes = 0) =>
  ({ id, name: id, displayName: id, status, podCount: pods, nodeCount: nodes }) as Cluster;

describe("fleetModel", () => {
  it("classifies every cluster exactly once so all overview counts agree", () => {
    const model = fleetModel(
      [
        cluster("a", "active", 20, 1),
        cluster("b", "active", 22, 2),
        cluster("c", "disconnected"),
        cluster("d", "error"),
        cluster("e", "pending"),
      ],
      5,
    );
    expect(model.counts).toEqual({
      healthy: 2,
      attention: 1,
      disconnected: 1,
      registering: 1,
    });
    const sum = Object.values(model.counts).reduce((a, b) => a + b, 0);
    expect(sum).toBe(model.total);
    expect(model.pods).toBe(42);
    expect(model.nodes).toBe(3);
    expect(model.truncated).toBe(false);
  });

  it("keeps the API total when the page is truncated", () => {
    const model = fleetModel([cluster("a", "active")], 250);
    expect(model.total).toBe(250);
    expect(model.truncated).toBe(true);
  });

  it("treats unknown statuses as needing attention", () => {
    expect(clusterHealth({ status: "unsupported" })).toBe("attention");
  });
});
