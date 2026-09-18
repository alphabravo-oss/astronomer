import type { MockedFunction } from "vitest";

import { listGenericClusterResources } from "@/lib/api/generated/client";
import { getGenericResources } from "@/lib/api/resource-search";

vi.mock("@/lib/api/generated/client", () => ({
  countClusterResources: vi.fn(),
  listGenericClusterResources: vi.fn(),
  searchResourcesAcrossClusters: vi.fn(),
}));

const mockedList = listGenericClusterResources as MockedFunction<
  typeof listGenericClusterResources
>;

describe("generic Kubernetes resource API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("keeps paging, namespace scope, search, and sorting server-side", async () => {
    mockedList.mockResolvedValueOnce({
      data: [
        {
          name: "config-b",
          namespace: "team-b",
          clusterId: "cluster-1",
        },
      ],
      pagination: {
        total: 33,
        limit: 20,
        offset: 20,
        has_more: false,
        next_offset: null,
      },
    });

    const result = await getGenericResources("cluster-1", "configmaps", {
      namespaces: "team-a,team-b",
      limit: 20,
      offset: 20,
      search: "config",
      sort: "name_desc",
    });

    expect(mockedList).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1", resource_type: "configmaps" },
      query: {
        namespace: undefined,
        namespaces: "team-a,team-b",
        limit: 20,
        offset: 20,
        search: "config",
        sort: "name_desc",
      },
      signal: undefined,
    });
    expect(result.pagination.total).toBe(33);
    expect(result.data[0].name).toBe("config-b");
  });
});
