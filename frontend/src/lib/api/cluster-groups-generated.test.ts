import {
  getClusterGroups,
  postClusterGroups,
  postClusterGroupsByIdMove,
} from "@/lib/api/generated/client";
import {
  createClusterGroup,
  listClusterGroups,
  moveClustersToGroup,
} from "./cluster-groups";

vi.mock("@/lib/api/generated/client", () => ({
  deleteClusterGroupsById: vi.fn(),
  getClusterGroups: vi.fn(),
  getClusterGroupsById: vi.fn(),
  getClusterGroupsByIdClusters: vi.fn(),
  postClusterGroups: vi.fn(),
  postClusterGroupsByIdMove: vi.fn(),
  putClusterGroupsById: vi.fn(),
}));

const groupWire = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "Production",
  slug: "production",
  description: "Production clusters",
  color: "#22c55e",
  icon: "server",
  enabled: true,
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T01:00:00Z",
  cluster_count: 2,
  cluster_count_tree: 3,
  depth: 1,
};

describe("generated cluster groups API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the exact tree wire shape and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(getClusterGroups).mockResolvedValueOnce({
      data: [groupWire],
      pagination: { limit: 1, offset: 0, has_more: false, next_offset: null },
    });

    await expect(listClusterGroups({ signal })).resolves.toEqual([
      expect.objectContaining({
        id: groupWire.id,
        parentId: undefined,
        clusterCountTree: 3,
        depth: 1,
      }),
    ]);
    expect(getClusterGroups).toHaveBeenCalledWith({ signal });
  });

  it("sends the generated create body without casing transforms", async () => {
    vi.mocked(postClusterGroups).mockResolvedValueOnce({ data: groupWire });
    const body = {
      name: "Production",
      parent_id: "00000000-0000-4000-8000-000000000002",
      color: "#22c55e",
    };

    await createClusterGroup(body);

    expect(postClusterGroups).toHaveBeenCalledWith({
      body,
      signal: undefined,
    });
  });

  it("maps the best-effort move result without inventing a receipt", async () => {
    vi.mocked(postClusterGroupsByIdMove).mockResolvedValueOnce({
      data: { moved: 1, skipped: ["not-a-uuid"] },
    });

    await expect(
      moveClustersToGroup("group-1", ["cluster-1", "not-a-uuid"]),
    ).resolves.toEqual({ moved: 1, skipped: ["not-a-uuid"] });
    expect(postClusterGroupsByIdMove).toHaveBeenCalledWith({
      path: { id: "group-1" },
      body: { cluster_ids: ["cluster-1", "not-a-uuid"] },
      signal: undefined,
    });
  });
});
