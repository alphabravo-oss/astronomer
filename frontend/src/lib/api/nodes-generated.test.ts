import { beforeEach, describe, expect, it, vi } from "vitest";

const generated = vi.hoisted(() => ({
  getClustersByClusterIdNodes: vi.fn(),
  getClustersByClusterIdNodesByNodeName: vi.fn(),
  getClustersByIdConditionRemediation: vi.fn(),
  getClustersByIdConditions: vi.fn(),
  getNodeOperation: vi.fn(),
  postClustersByIdOwnershipTakeover: vi.fn(),
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

import {
  getClusterConditionRemediation,
  getClusterNodes,
  getNodeDetail,
  takeoverClusterOwnership,
} from "./nodes";

describe("generated node reads", () => {
  beforeEach(() => vi.clearAllMocks());

  it("walks every node page and preserves the AbortSignal", async () => {
    const signal = new AbortController().signal;
    generated.getClustersByClusterIdNodes
      .mockResolvedValueOnce({
        data: [
          {
            name: "worker-1",
            status: "Ready",
            roles: ["worker"],
            conditions: [
              {
                type: "Ready",
                status: "True",
                lastTransition: "2026-08-24T00:00:00Z",
              },
            ],
          },
        ],
        pagination: {
          limit: 200,
          offset: 0,
          has_more: true,
          next_offset: 200,
        },
      })
      .mockResolvedValueOnce({
        data: [{ name: "worker-201", status: "SchedulingDisabled" }],
        pagination: {
          limit: 200,
          offset: 200,
          has_more: false,
          next_offset: null,
        },
      });

    await expect(getClusterNodes("cluster-1", { signal })).resolves.toEqual([
      expect.objectContaining({ name: "worker-1", status: "Ready" }),
      expect.objectContaining({
        name: "worker-201",
        status: "SchedulingDisabled",
      }),
    ]);
    expect(generated.getClustersByClusterIdNodes).toHaveBeenNthCalledWith(2, {
      path: { cluster_id: "cluster-1" },
      query: { limit: 200, offset: 200 },
      signal,
    });
  });

  it("maps Kubernetes nodeInfo and condition keys explicitly", async () => {
    generated.getClustersByClusterIdNodesByNodeName.mockResolvedValue({
      data: {
        name: "worker-1",
        status: "Ready",
        nodeInfo: {
          machineID: "machine-1",
          systemUUID: "system-1",
          bootID: "boot-1",
          kubeletVersion: "v1.33.1",
        },
        conditions: [
          {
            type: "Ready",
            status: "True",
            lastHeartbeatTime: "2026-08-24T00:01:00Z",
            lastTransitionTime: "2026-08-24T00:00:00Z",
          },
        ],
      },
    });

    await expect(getNodeDetail("cluster-1", "worker-1")).resolves.toMatchObject(
      {
        nodeInfo: {
          machineId: "machine-1",
          systemUuid: "system-1",
          bootId: "boot-1",
          kubeletVersion: "v1.33.1",
        },
        conditions: [
          {
            lastHeartbeat: "2026-08-24T00:01:00Z",
            lastTransition: "2026-08-24T00:00:00Z",
          },
        ],
      },
    );
  });

  it("maps remediation nullability and ownership wire fields", async () => {
    generated.getClustersByIdConditionRemediation.mockResolvedValue({
      data: [
        {
          id: "attempt-1",
          cluster_id: "cluster-1",
          condition_type: "Connected",
          action: "rotate_token",
          outcome: "success",
          error: "",
          detail: null,
          attempted_at: "2026-08-24T00:00:00Z",
        },
      ],
      pagination: {
        limit: 1,
        offset: 0,
        has_more: false,
        next_offset: null,
      },
    });
    generated.postClustersByIdOwnershipTakeover.mockResolvedValue({
      data: { id: "cluster-1", managed_by: "api", transferred: true },
    });

    await expect(getClusterConditionRemediation("cluster-1")).resolves.toEqual([
      expect.objectContaining({ error: null, detail: null }),
    ]);
    await expect(takeoverClusterOwnership("cluster-1")).resolves.toEqual({
      id: "cluster-1",
      managedBy: "api",
      transferred: true,
    });
  });
});
