import type { MockedFunction } from "vitest";
import {
  getClustersByClusterIdWorkloads,
  getWorkloadsPodsByClusterIdByNamespaceByPodLogs,
  patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale,
  postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart,
} from "@/lib/api/generated/client";
import {
  getPodLogs,
  getWorkloads,
  restartWorkload,
  scaleWorkload,
} from "./workloads";

vi.mock("@/lib/api/generated/client", () => ({
  deleteWorkloadsPodsByClusterIdByNamespaceByPod: vi.fn(),
  getClustersByClusterIdEvents: vi.fn(),
  getClustersByClusterIdNamespaces: vi.fn(),
  getClustersByClusterIdPods: vi.fn(),
  getClustersByClusterIdWorkloads: vi.fn(),
  getClustersByClusterIdWorkloadsByKindByNamespaceByName: vi.fn(),
  getClustersByClusterIdWorkloadsByKindByNamespaceByNamePods: vi.fn(),
  getWorkloadsOperationsById: vi.fn(),
  getWorkloadsPodsByClusterIdByNamespaceByPodLogs: vi.fn(),
  patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale: vi.fn(),
  postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart: vi.fn(),
}));

const mockedList = getClustersByClusterIdWorkloads as MockedFunction<
  typeof getClustersByClusterIdWorkloads
>;
const mockedLogs =
  getWorkloadsPodsByClusterIdByNamespaceByPodLogs as MockedFunction<
    typeof getWorkloadsPodsByClusterIdByNamespaceByPodLogs
  >;
const mockedScale =
  patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale as MockedFunction<
    typeof patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale
  >;
const mockedRestart =
  postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart as MockedFunction<
    typeof postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart
  >;

const wireWorkload = {
  name: "api",
  namespace: "default",
  kind: "Deployment",
  clusterId: "cluster-1",
  clusterName: "prod",
  status: "Running",
  ready: "2/2",
  upToDate: 2,
  available: 2,
  replicas: 2,
  desiredReplicas: 2,
  images: ["api:v1"],
  labels: {},
  annotations: {},
  createdAt: "2026-08-24T00:00:00Z",
  age: "1d",
};

describe("workloads generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("translates page pagination to limit/offset and restores the view page", async () => {
    mockedList.mockResolvedValueOnce({
      data: [wireWorkload],
      count: 41,
      next: "/next",
      previous: "/previous",
    });

    const result = await getWorkloads("cluster-1", {
      namespace: "default",
      page: 3,
      pageSize: 20,
    });

    expect(mockedList).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
      query: {
        limit: 20,
        offset: 40,
        namespace: "default",
        kind: undefined,
        search: undefined,
      },
      signal: undefined,
    });
    expect(result).toMatchObject({
      total: 41,
      page: 3,
      pageSize: 20,
      totalPages: 3,
    });
    expect(result.data[0].name).toBe("api");
  });

  it("sends caller-stable idempotency on scale and maps the receipt", async () => {
    const controller = new AbortController();
    mockedScale.mockResolvedValueOnce({
      data: { id: "operation-1", status: "pending" },
    });

    const result = await scaleWorkload(
      "cluster-1",
      "Deployment",
      "default",
      "api",
      4,
      { idempotencyKey: "scale-once", signal: controller.signal },
    );

    expect(mockedScale).toHaveBeenCalledWith({
      path: {
        cluster_id: "cluster-1",
        kind: "Deployment",
        namespace: "default",
        name: "api",
      },
      headerParams: { "Idempotency-Key": "scale-once" },
      body: { replicas: 4 },
      signal: controller.signal,
    });
    expect(result).toEqual({
      id: "operation-1",
      status: "pending",
      errorMessage: undefined,
    });
  });

  it("sends caller-stable idempotency on restart", async () => {
    mockedRestart.mockResolvedValueOnce({
      data: { id: "operation-2", status: "pending" },
    });

    await restartWorkload("cluster-1", "Deployment", "default", "api", {
      idempotencyKey: "restart-once",
    });

    expect(mockedRestart).toHaveBeenCalledWith(
      expect.objectContaining({
        headerParams: { "Idempotency-Key": "restart-once" },
      }),
    );
  });

  it("forwards the generated log-query casing and AbortSignal", async () => {
    const controller = new AbortController();
    mockedLogs.mockResolvedValueOnce({
      data: [{ timestamp: "now", message: "ready", container: "api" }],
    });

    const result = await getPodLogs("cluster-1", "default", "api-0", {
      container: "api",
      tailLines: 100,
      sinceSeconds: 60,
      signal: controller.signal,
    });

    expect(mockedLogs).toHaveBeenCalledWith({
      path: {
        cluster_id: "cluster-1",
        namespace: "default",
        pod: "api-0",
      },
      query: {
        container: "api",
        tailLines: 100,
        sinceSeconds: 60,
        follow: undefined,
      },
      signal: controller.signal,
    });
    expect(result[0]).toEqual({
      timestamp: "now",
      message: "ready",
      container: "api",
    });
  });
});
