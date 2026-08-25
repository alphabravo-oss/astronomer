import { describe, expect, it } from "vitest";

import { createPathForManifest } from "./create-resource-dialog";

describe("create resource API path", () => {
  it("prefers live discovery over handwritten kind routing", () => {
    const path = createPathForManifest(
      {
        apiVersion: "apps/v1",
        kind: "Deployment",
        metadata: { name: "web", namespace: "team-a" },
      },
      {
        resource: {
          resourceType: "deployments",
          apiBase: "/apis/apps/v1",
          apiVersion: "v1",
          kind: "Deployment",
          plural: "deployments",
          namespaced: true,
          verbs: ["create"],
          policy: {
            secret: false,
            privilegeEscalating: false,
            destructiveDelete: false,
            forceConflictPermission: true,
          },
          source: "kubernetes_discovery",
        },
        schema: {},
        schemaAvailable: true,
        definitions: {},
        definitionsTruncated: false,
      },
    );
    expect(path).toBe("apis/apps/v1/namespaces/team-a/deployments");
  });

  it("uses the API version fallback for cluster-scoped resources", () => {
    expect(
      createPathForManifest({
        apiVersion: "v1",
        kind: "Namespace",
        metadata: { name: "team-a" },
      }),
    ).toBe("api/v1/namespaces");
  });
});
