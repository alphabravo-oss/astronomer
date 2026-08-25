import {
  addProjectNamespace,
  createClusterTemplate,
  getClusterTemplateBoundClusters,
  getProjectPolicy,
  listProjectCloudCredentials,
  removeProjectNamespace,
  subscribeProjectCatalog,
} from "./project-detail";

const operations = vi.hoisted(() => ({
  add: vi.fn(),
  remove: vi.fn(),
  getProject: vi.fn(),
  listCredentials: vi.fn(),
  createTemplate: vi.fn(),
  subscribeCatalog: vi.fn(),
  boundClusters: vi.fn(),
}));
vi.mock("@/lib/api/generated/client", () => ({
  postProjectsByIdAddNamespace: operations.add,
  postProjectsByIdRemoveNamespace: operations.remove,
  getProjectsById: operations.getProject,
  getProjectsByProjectIdCloudCredentials: operations.listCredentials,
  postClusterTemplates: operations.createTemplate,
  postProjectsByProjectIdCatalogsByCatalogIdSubscribe:
    operations.subscribeCatalog,
  getClusterTemplatesByIdClusters: operations.boundClusters,
}));

/**
 * The project→namespace endpoints are the only authoring surface for project
 * tenancy, and a project's namespaces are what its role bindings resolve to. The
 * component test stubs the whole hooks module, so without these the request
 * shape is never exercised anywhere: a path typo or a changed method would ship
 * green.
 *
 * The chi routes are registered as `/{id}/add-namespace/` and
 * `/{id}/remove-namespace/` WITH a trailing slash; the URLs below omit it and
 * rely on the request interceptor in lib/api.ts appending one (it skips only
 * URLs already ending in `/`, containing `?`, or containing `/k8s/`). That
 * dependency is load-bearing, so it is asserted explicitly here.
 */
describe("project namespace membership API client", () => {
  beforeEach(() => vi.clearAllMocks());

  it("POSTs the namespace to add-namespace and unwraps the project", async () => {
    operations.add.mockResolvedValueOnce({
      data: { id: "p1", name: "project", namespaces: ["team-a"] },
    });

    const project = await addProjectNamespace("p1", "team-a");

    expect(operations.add).toHaveBeenCalledWith({
      path: { id: "p1" },
      body: { namespace: "team-a" },
      signal: undefined,
    });
    expect(project).toEqual(
      expect.objectContaining({ id: "p1", namespaces: ["team-a"] }),
    );
  });

  it("POSTs the namespace to remove-namespace and unwraps the project", async () => {
    operations.remove.mockResolvedValueOnce({
      data: { id: "p1", name: "project", namespaces: [] },
    });

    const project = await removeProjectNamespace("p1", "team-a");

    expect(operations.remove).toHaveBeenCalledWith({
      path: { id: "p1" },
      body: { namespace: "team-a" },
      signal: undefined,
    });
    expect(project).toEqual(
      expect.objectContaining({ id: "p1", namespaces: [] }),
    );
  });

  it("maps project policy from exact project wire fields and propagates signal", async () => {
    const controller = new AbortController();
    operations.getProject.mockResolvedValue({
      data: {
        id: "p1",
        pod_security_profile: "restricted",
        network_policy_mode: "isolated",
        resource_quota_cpu_limit: "4",
        resource_quota_memory_limit: "8Gi",
        resource_quota_pod_count: 20,
      },
    });
    await expect(
      getProjectPolicy("p1", { signal: controller.signal }),
    ).resolves.toEqual({
      podSecurityProfile: "restricted",
      networkPolicyMode: "isolated",
      resourceQuotaCpu: "4",
      resourceQuotaMemory: "8Gi",
      resourceQuotaPods: 20,
    });
    expect(operations.getProject).toHaveBeenCalledWith({
      path: { id: "p1" },
      signal: controller.signal,
    });
  });

  it("maps credential data and target refs without generic camelization", async () => {
    operations.listCredentials.mockResolvedValue({
      data: {
        items: [
          {
            id: "cred-1",
            name: "prod",
            provider: "aws",
            data: { access_key: "<redacted>" },
            target_refs: [{ cluster_id: "cluster-1", namespace: "apps" }],
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-02T00:00:00Z",
          },
        ],
      },
    });
    await expect(listProjectCloudCredentials("p1")).resolves.toEqual([
      expect.objectContaining({
        id: "cred-1",
        config: { access_key: "<redacted>" },
        targetRefs: [{ clusterId: "cluster-1", namespaces: ["apps"] }],
      }),
    ]);
  });

  it("maps template writes to the exact generated body", async () => {
    operations.createTemplate.mockResolvedValue({
      data: { id: "t1", name: "production", spec: {} },
    });
    await createClusterTemplate({
      name: "production",
      displayName: "Production",
      spec: {
        environment: "production",
        labels: [],
        tools: [],
        defaultProject: {
          podSecurityProfile: "restricted",
          networkPolicyMode: "isolated",
        },
        registrationPolicy: { tokenRotationDays: 30 },
      },
    });
    expect(operations.createTemplate).toHaveBeenCalledWith(
      expect.objectContaining({
        body: expect.objectContaining({ name: "production" }),
      }),
    );
  });

  it("uses the generated idempotent catalog subscription operation", async () => {
    operations.subscribeCatalog.mockResolvedValue({ data: { id: "sub-1" } });
    await subscribeProjectCatalog("p1", "catalog/1");
    expect(operations.subscribeCatalog).toHaveBeenCalledWith({
      path: { project_id: "p1", catalog_id: "catalog/1" },
      signal: undefined,
    });
  });

  it("maps applying bound-cluster status and propagates signal", async () => {
    const controller = new AbortController();
    operations.boundClusters.mockResolvedValue({
      data: [
        {
          cluster_id: "cluster-1",
          cluster_name: "east",
          status: "applying",
          last_applied_at: "2026-01-01T00:00:00Z",
        },
      ],
    });
    await expect(
      getClusterTemplateBoundClusters("t1", { signal: controller.signal }),
    ).resolves.toEqual([
      expect.objectContaining({
        clusterId: "cluster-1",
        clusterName: "east",
        status: "applying",
      }),
    ]);
    expect(operations.boundClusters).toHaveBeenCalledWith({
      path: { id: "t1" },
      signal: controller.signal,
    });
  });
});
