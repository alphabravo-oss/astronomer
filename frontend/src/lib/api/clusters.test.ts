import { beforeEach, describe, expect, it, vi } from "vitest";

const { list, get, create, update, remove } = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
}));
vi.mock("@/lib/api/generated/client", () => ({
  getClusters: list,
  getClustersById: get,
  postClusters: create,
  patchClustersById: update,
  deleteClustersById: remove,
}));

import {
  createCluster,
  getClusters,
  mapCluster,
  updateCluster,
} from "./clusters";
import type { OpenAPIComponents } from "@/types/openapi.generated";

const wire: OpenAPIComponents["schemas"]["Cluster"] = {
  id: "cluster-1",
  name: "west-prod",
  display_name: "West production",
  description: "",
  status: "active",
  api_server_url: "https://kubernetes.example.test",
  ca_certificate: "public-ca",
  environment: "production",
  region: "us-west-2",
  provider: "eks",
  distribution: "eks",
  labels: { team: "platform" },
  annotations: {},
  registration_phase: "ready",
  install_baseline: true,
  agent_version: "1.1.0",
  kubernetes_version: "1.35.1",
  node_count: 6,
  last_heartbeat: "2026-08-23T00:00:00Z",
  is_local: false,
  decommissioned_at: null,
  created_by_id: null,
  cluster_uid: "upstream-uid",
  group_id: null,
  registration_started_at: null,
  registration_completed_at: null,
  managed_by: "api",
  external_ref_api_version: "",
  external_ref_kind: "",
  external_ref_namespace: "",
  external_ref_name: "",
  observed_generation: 7,
  cpu_percentage: 31,
  memory_percentage: 48,
  pod_count: 120,
  metrics_server_present: true,
  decommissioning: false,
  agent_privilege_profile: "operator",
  downstream_impersonation: "off",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
};

describe("cluster API mapper", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    list.mockResolvedValue({
      data: [wire],
      count: 1,
      next: null,
      previous: null,
    });
    create.mockResolvedValue({ data: wire });
    update.mockResolvedValue({ data: wire });
  });

  it("maps the exact snake_case DTO and does not invent metrics capacity fields", () => {
    const cluster = mapCluster(wire);
    expect(cluster.displayName).toBe("West production");
    expect(cluster.agentPrivilegeProfile).toBe("operator");
    expect(cluster.metricsServerPresent).toBe(true);
    expect(cluster.cpuCapacity).toBeUndefined();
  });

  it("maps authorized pagination totals and all list filters", async () => {
    const page = await getClusters({
      status: "active",
      provider: "eks",
      search: "west",
      page: 2,
      pageSize: 25,
    });
    expect(list).toHaveBeenCalledWith({
      query: {
        status: "active",
        provider: "eks",
        environment: undefined,
        search: "west",
        limit: 25,
        offset: 25,
      },
    });
    expect(page).toMatchObject({
      total: 1,
      count: 1,
      page: 2,
      pageSize: 25,
      totalPages: 1,
    });
  });

  it("creates with generated snake_case fields and drops wizard-only fields", async () => {
    await createCluster({
      name: "west-prod",
      displayName: "West production",
      environment: "production",
      provider: "eks",
      install_baseline: true,
      annotations: { "astronomer.io/profile": "operator" },
      apiServerUrl: "https://api.example.test:6443",
      caCertificate: "public-ca",
    });
    expect(create).toHaveBeenCalledWith({
      body: expect.objectContaining({
        name: "west-prod",
        display_name: "West production",
        environment: "production",
        provider: "eks",
        annotations: { "astronomer.io/profile": "operator" },
        api_server_url: "https://api.example.test:6443",
        ca_certificate: "public-ca",
      }),
    });
    expect(create.mock.calls[0][0].body).not.toHaveProperty("install_baseline");
  });

  it("sends an exact one-field partial update without clearing omitted fields", async () => {
    await updateCluster("cluster-1", { description: "Changed only" });
    expect(update).toHaveBeenCalledWith({
      path: { id: "cluster-1" },
      body: { description: "Changed only" },
    });
  });
});
