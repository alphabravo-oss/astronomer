import { beforeEach, describe, expect, it, vi } from "vitest";

const generated = vi.hoisted(() => ({
  getNodeOperation: vi.fn(),
  postNodesByClusterIdByNodeNameAnnotations: vi.fn(),
  postNodesByClusterIdByNodeNameAnnotationsRemove: vi.fn(),
  postNodesByClusterIdByNodeNameCordon: vi.fn(),
  postNodesByClusterIdByNodeNameDrain: vi.fn(),
  postNodesByClusterIdByNodeNameLabels: vi.fn(),
  postNodesByClusterIdByNodeNameLabelsRemove: vi.fn(),
  postNodesByClusterIdByNodeNameTaints: vi.fn(),
  postNodesByClusterIdByNodeNameTaintsRemove: vi.fn(),
  postNodesByClusterIdByNodeNameUncordon: vi.fn(),
}));

vi.mock("@/lib/api/generated/client", () => generated);

import { getNodeOperationStatus, submitNodeOperation } from "./nodes";

const operation = {
  id: "node-op-1",
  cluster_id: "cluster-1",
  node_name: "worker-1",
  action: "cordon",
  status: "pending",
  generation: 1,
  observed_generation: 0,
  attempt_count: 0,
  progress: {},
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T00:00:00Z",
};

describe("node durable operations", () => {
  beforeEach(() => vi.clearAllMocks());

  it("submits through the generated client with the exact idempotency key", async () => {
    const controller = new AbortController();
    generated.postNodesByClusterIdByNodeNameCordon.mockResolvedValue({
      data: operation,
    });

    await expect(
      submitNodeOperation(
        { clusterId: "cluster-1", nodeName: "worker-1", action: "cordon" },
        { idempotencyKey: "node:request-1", signal: controller.signal },
      ),
    ).resolves.toMatchObject({
      id: "node-op-1",
      clusterId: "cluster-1",
      nodeName: "worker-1",
      status: "pending",
    });
    expect(generated.postNodesByClusterIdByNodeNameCordon).toHaveBeenCalledWith(
      {
        path: { cluster_id: "cluster-1", node_name: "worker-1" },
        headerParams: { "Idempotency-Key": "node:request-1" },
        signal: controller.signal,
      },
    );
  });

  it("surfaces drain blockers from operation progress", async () => {
    generated.getNodeOperation.mockResolvedValue({
      data: {
        ...operation,
        action: "drain",
        status: "blocked",
        progress: { blockers: ["default/api:empty_dir"] },
      },
    });

    await expect(
      getNodeOperationStatus("node-op-1", undefined, {
        id: "node-op-1",
        clusterId: "cluster-1",
        nodeName: "worker-1",
        action: "drain",
        status: "running",
        progress: {},
      }),
    ).resolves.toMatchObject({
      status: "blocked",
      errorMessage: "Drain blocked by default/api:empty_dir",
    });
  });

  it("rejects the legacy synchronous drain response", async () => {
    generated.postNodesByClusterIdByNodeNameDrain.mockResolvedValue({
      data: { node: "worker-1", status: "drained" },
    });
    await expect(
      submitNodeOperation(
        { clusterId: "cluster-1", nodeName: "worker-1", action: "drain" },
        { idempotencyKey: "node:request-2" },
      ),
    ).rejects.toThrow("durable operation receipt");
  });
});
