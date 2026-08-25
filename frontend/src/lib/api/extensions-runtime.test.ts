import type { MockedFunction } from "vitest";
import {
  getExtensionsMounts,
  postExtensionsByNameDataByDataSourceId,
  postExtensionsByNameToken,
} from "@/lib/api/generated/client";
import {
  fetchExtensionData,
  getExtensionMounts,
  requestExtensionBridgeToken,
} from "./extensions";

vi.mock("@/lib/api/generated/client", () => ({
  getExtensionsMounts: vi.fn(),
  postExtensionsByNameDataByDataSourceId: vi.fn(),
  postExtensionsByNameToken: vi.fn(),
}));

const mockedMounts = getExtensionsMounts as MockedFunction<
  typeof getExtensionsMounts
>;
const mockedData = postExtensionsByNameDataByDataSourceId as MockedFunction<
  typeof postExtensionsByNameDataByDataSourceId
>;
const mockedToken = postExtensionsByNameToken as MockedFunction<
  typeof postExtensionsByNameToken
>;

describe("extensions host-runtime generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("normalizes mounts and forwards AbortSignal", async () => {
    const controller = new AbortController();
    mockedMounts.mockResolvedValueOnce({
      data: {
        clusterTabs: [
          {
            extension: "cost",
            displayName: "Cost",
            point: "clusterTab",
            pointId: "CostTab",
            title: "Cost",
            tier: 1,
            render: { declarative: { kind: "stat" } },
            dataSources: [{ id: "pod-cost", shape: "object" }],
          },
        ],
      },
    });

    const result = await getExtensionMounts({ signal: controller.signal });

    expect(mockedMounts).toHaveBeenCalledWith({ signal: controller.signal });
    expect(result.sidebar).toEqual([]);
    expect(result.clusterTabs[0]).toMatchObject({
      extension: "cost",
      label: "Cost",
      pointId: "CostTab",
    });
  });

  it("uses generated path parameters and maps data metadata", async () => {
    mockedData.mockResolvedValueOnce({
      data: {
        data: { rows: [] },
        shape: "list",
        meta: { dataSourceId: "pod cost", rows: 0, cached: true },
      },
    });
    const request = {
      context: { clusterId: "c1" },
      query: { window: "7d" },
    };

    const result = await fetchExtensionData(
      "cost insights",
      "pod cost",
      request,
    );

    expect(mockedData).toHaveBeenCalledWith({
      path: { name: "cost insights", dataSourceId: "pod cost" },
      body: {
        context: {
          clusterId: "c1",
          projectId: undefined,
          namespace: undefined,
        },
        pathParams: undefined,
        query: { window: "7d" },
        body: undefined,
      },
      signal: undefined,
    });
    expect(result).toMatchObject({
      shape: "list",
      meta: { dataSourceId: "pod cost", rows: 0, cached: true },
    });
  });

  it("defaults the data-proxy request to an empty generated body", async () => {
    mockedData.mockResolvedValueOnce({
      data: { data: {}, shape: "object", meta: {} },
    });

    await fetchExtensionData("ext", "d1");

    expect(mockedData).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { name: "ext", dataSourceId: "d1" },
        body: {
          context: undefined,
          pathParams: undefined,
          query: undefined,
          body: undefined,
        },
      }),
    );
  });

  it("maps bridge-token fields and preserves the scoped context", async () => {
    mockedToken.mockResolvedValueOnce({
      data: {
        token: "opaque",
        dataSource: "podCost",
        expiresAt: "2026-06-25T12:00:00Z",
        scope: "ext:cost-insights:data:podCost",
      },
    });

    const result = await requestExtensionBridgeToken(
      "cost-insights",
      "podCost",
      { clusterId: "c1" },
    );

    expect(mockedToken).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { name: "cost-insights" },
        body: {
          dataSource: "podCost",
          context: {
            clusterId: "c1",
            projectId: undefined,
            namespace: undefined,
          },
        },
      }),
    );
    expect(result.token).toBe("opaque");
    expect(result.scope).toBe("ext:cost-insights:data:podCost");
  });
});
