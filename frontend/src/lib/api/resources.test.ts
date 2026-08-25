import { beforeEach, describe, expect, it, vi } from "vitest";

const { discovery, schema } = vi.hoisted(() => ({
  discovery: vi.fn(),
  schema: vi.fn(),
}));

vi.mock("@/lib/api/generated/client", () => ({
  getClustersByClusterIdResourcesDiscovery: discovery,
  getClustersByClusterIdResourcesSchema: schema,
}));

import { getResourceDiscovery, getResourceSchema } from "./resources";

const wireEntry = {
  resource_type: "deployments",
  api_base: "/apis/apps/v1",
  api_group: "apps",
  api_version: "v1",
  kind: "Deployment",
  plural: "deployments",
  namespaced: true,
  verbs: ["get", "create"],
  short_names: ["deploy"],
  categories: ["all"],
  policy: {
    secret: false,
    privilege_escalating: false,
    destructive_delete: false,
    force_conflict_permission: true,
  },
  source: "kubernetes_discovery",
} as const;

beforeEach(() => {
  discovery.mockReset();
  schema.mockReset();
});

describe("resource discovery generated boundary", () => {
  it("maps discovery wire casing explicitly", async () => {
    discovery.mockResolvedValue({
      data: {
        cluster_id: "cluster-1",
        resources: [wireEntry],
        crds: [],
        partial: false,
        errors: {},
      },
    });

    const result = await getResourceDiscovery("cluster-1");
    expect(discovery).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
    });
    expect(result.resources[0]).toMatchObject({
      resourceType: "deployments",
      apiBase: "/apis/apps/v1",
      policy: { forceConflictPermission: true },
    });
  });

  it("preserves raw Kubernetes schema and definition keys", async () => {
    const root = {
      properties: {
        spec: { $ref: "#/components/schemas/io.k8s.DeploymentSpec" },
      },
      "x-kubernetes-group-version-kind": [{ kind: "Deployment" }],
    };
    schema.mockResolvedValue({
      data: {
        resource: wireEntry,
        schema: root,
        schema_name: "io.k8s.Deployment",
        schema_available: true,
        definitions: {
          "io.k8s.DeploymentSpec": {
            properties: { minReadySeconds: { type: "integer" } },
          },
        },
        definitions_truncated: false,
      },
    });

    const result = await getResourceSchema("cluster-1", "deployments");
    expect(schema).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
      query: { resource_type: "deployments" },
    });
    expect(result.schema).toBe(root);
    expect(result.definitions["io.k8s.DeploymentSpec"]).toHaveProperty(
      "properties.minReadySeconds",
    );
  });
});
