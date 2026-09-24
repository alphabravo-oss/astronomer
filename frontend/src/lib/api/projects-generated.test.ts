import {
  getClustersByClusterIdProjects,
  getProjects,
  postProjects,
  postProjectsByIdOwnershipTakeover,
} from "@/lib/api/generated/client";
import {
  createProject,
  getClusterProjects,
  getProjects as listProjects,
  takeoverProjectOwnership,
} from "./projects";

vi.mock("@/lib/api/generated/client", () => ({
  deleteProjectsById: vi.fn(),
  getClustersByClusterIdProjects: vi.fn(),
  getProjects: vi.fn(),
  getProjectsById: vi.fn(),
  postProjects: vi.fn(),
  postProjectsByIdOwnershipTakeover: vi.fn(),
  putProjectsById: vi.fn(),
}));

const projectWire = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "production",
  display_name: "Production",
  description: "Production workloads",
  cluster_id: "00000000-0000-4000-8000-000000000002",
  namespaces: ["apps"],
  resource_quota: { "requests.storage": "20Gi" },
  limit_range: {},
  network_policy_mode: "isolated" as const,
  pod_security_profile: "restricted",
  resource_quota_cpu_limit: "4",
  resource_quota_memory_limit: "8Gi",
  resource_quota_pod_count: 20,
  created_by_id: null,
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T01:00:00Z",
};

describe("generated projects API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps exact pagination and project wire fields with cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(getProjects).mockResolvedValueOnce({
      data: [projectWire],
      pagination: {
        total: 21,
        limit: 20,
        offset: 20,
        has_more: false,
        next_offset: null,
      },
    });

    await expect(
      listProjects({ page: 2, pageSize: 20 }, { signal }),
    ).resolves.toEqual(
      expect.objectContaining({
        pagination: {
          total: 21,
          limit: 20,
          offset: 20,
          has_more: false,
          next_offset: null,
        },
        data: [
          expect.objectContaining({
            displayName: "Production",
            clusterId: projectWire.cluster_id,
            clusterIds: [projectWire.cluster_id],
            resourceQuota: expect.objectContaining({ storageLimit: "20Gi" }),
          }),
        ],
      }),
    );
    expect(getProjects).toHaveBeenCalledWith({
      query: { limit: 20, offset: 20 },
      signal,
    });
  });

  it("sends bounded cluster project search through the generated operation", async () => {
    vi.mocked(getClustersByClusterIdProjects).mockResolvedValueOnce({
      data: [projectWire],
      pagination: {
        total: 1,
        limit: 50,
        offset: 200,
        has_more: false,
        next_offset: null,
      },
    });

    await expect(
      getClusterProjects("cluster-1", {
        page: 5,
        pageSize: 50,
        search: " production ",
      }),
    ).resolves.toMatchObject({ data: [{ id: projectWire.id }] });
    expect(getClustersByClusterIdProjects).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
      query: { limit: 50, offset: 200, search: "production" },
      signal: undefined,
    });
  });

  it("translates the legacy UI cluster array into required cluster_id", async () => {
    vi.mocked(postProjects).mockResolvedValueOnce({ data: projectWire });

    await createProject({
      name: "production",
      displayName: "Production",
      clusterIds: [projectWire.cluster_id],
      namespaces: ["apps"],
    });

    expect(postProjects).toHaveBeenCalledWith({
      body: expect.objectContaining({
        name: "production",
        display_name: "Production",
        cluster_id: projectWire.cluster_id,
        namespaces: ["apps"],
      }),
      signal: undefined,
    });
  });

  it("maps the ownership-transfer result without inventing a receipt", async () => {
    vi.mocked(postProjectsByIdOwnershipTakeover).mockResolvedValueOnce({
      data: { id: projectWire.id, managed_by: "api", transferred: true },
    });
    await expect(takeoverProjectOwnership(projectWire.id)).resolves.toEqual({
      id: projectWire.id,
      managedBy: "api",
      transferred: true,
    });
  });
  it("maps authoritative secondary membership and preserves per-cluster namespaces", async () => {
    vi.mocked(getProjects).mockResolvedValueOnce({
      data: [
        {
          ...projectWire,
          cluster_ids: [projectWire.cluster_id, "secondary"],
          namespace_scopes: [
            { cluster_id: projectWire.cluster_id, namespaces: ["apps"] },
            { cluster_id: "secondary", namespaces: ["secondary-apps"] },
          ],
        },
      ],
      pagination: {
        limit: 20,
        offset: 0,
        total: 1,
        has_more: false,
        next_offset: null,
      },
    });
    const result = await listProjects();
    expect(result.data[0].clusterIds).toContain("secondary");
    expect(result.data[0].namespaceScopes).toContainEqual({
      clusterId: "secondary",
      namespaces: ["secondary-apps"],
    });
    expect(result.data[0].namespaces).toEqual(["apps"]);
  });
});
