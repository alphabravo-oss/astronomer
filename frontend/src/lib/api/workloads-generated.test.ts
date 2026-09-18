import type { MockedFunction } from "vitest";
import {
  getClustersByClusterIdNamespaces,
  getClustersByClusterIdPods,
  getClustersByClusterIdWorkloads,
  getWorkloadsPodsByClusterIdByNamespaceByPodLogs,
  patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale,
  postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart,
} from "@/lib/api/generated/client";
import {
  getPodLogs,
  getClusterPods,
  getClusterNamespaces,
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
const mockedNamespaces = getClustersByClusterIdNamespaces as MockedFunction<
  typeof getClustersByClusterIdNamespaces
>;
const mockedPods = getClustersByClusterIdPods as MockedFunction<
  typeof getClustersByClusterIdPods
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
      pagination: {
        total: 41,
        limit: 20,
        offset: 40,
        has_more: false,
        next_offset: null,
      },
    });

    const result = await getWorkloads("cluster-1", {
      namespace: "default",
      sort: "name_desc",
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
        sort: "name_desc",
      },
      signal: undefined,
    });
    expect(result).toMatchObject({
      pagination: {
        total: 41,
        limit: 20,
        offset: 40,
        has_more: false,
        next_offset: null,
      },
    });
    expect(result.data[0].name).toBe("api");
  });

  it("loads every authorized namespace page beyond the former first 20", async () => {
    mockedNamespaces.mockImplementation(async ({ query }) => {
      const offset = query?.offset ?? 0;
      const count = offset === 0 ? 200 : 25;
      return {
        data: Array.from({ length: count }, (_, index) => ({
          name: `namespace-${offset + index + 1}`,
          clusterId: "cluster-1",
          status: "Active",
          createdAt: "2026-08-24T00:00:00Z",
        })),
        pagination: {
          total: 225,
          limit: 200,
          offset,
          has_more: offset === 0,
          next_offset: offset === 0 ? 200 : null,
        },
      };
    });

    const namespaces = await getClusterNamespaces("cluster-1");

    expect(namespaces).toHaveLength(225);
    expect(namespaces.at(-1)?.name).toBe("namespace-225");
    expect(mockedNamespaces.mock.calls.map(([args]) => args.query)).toEqual([
      { limit: 200, offset: 0 },
      { limit: 200, offset: 200 },
    ]);
  });

  it("sends pod paging, search, sort, and health to the server", async () => {
    mockedPods.mockResolvedValueOnce({
      data: [
        {
          name: "api-1",
          namespace: "default",
          clusterId: "cluster-1",
          phase: "Running",
          status: "Running",
          createdAt: "2026-09-18T00:00:00Z",
        },
      ],
      pagination: {
        total: 101,
        limit: 20,
        offset: 40,
        has_more: true,
        next_offset: 60,
      },
    });

    const result = await getClusterPods("cluster-1", {
      limit: 20,
      offset: 40,
      search: "api",
      sort: "restarts_desc",
      health: "restarted",
    });

    expect(mockedPods).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
      query: {
        namespace: undefined,
        limit: 20,
        offset: 40,
        search: "api",
        sort: "restarts_desc",
        health: "restarted",
      },
      signal: undefined,
    });
    expect(result.pagination.total).toBe(101);
    expect(result.data[0].name).toBe("api-1");
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
