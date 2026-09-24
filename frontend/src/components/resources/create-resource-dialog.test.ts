import { describe, expect, it } from "vitest";

import {
  createPathForManifest,
  normalizeManifestDocuments,
} from "./create-resource-manifest";

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

describe("multi-document manifest normalization", () => {
  it("drops empty separators and retains document order", () => {
    const deployment = { apiVersion: "apps/v1", kind: "Deployment" };
    const service = { apiVersion: "v1", kind: "Service" };

    expect(
      normalizeManifestDocuments([null, deployment, undefined, service]),
    ).toEqual([deployment, service]);
  });

  it("rejects empty, scalar, and unbounded document sets", () => {
    expect(() => normalizeManifestDocuments([])).toThrow(
      "at least one Kubernetes object",
    );
    expect(() => normalizeManifestDocuments(["not-an-object"])).toThrow(
      "document 1",
    );
    expect(() =>
      normalizeManifestDocuments(
        Array.from({ length: 51 }, () => ({ kind: "ConfigMap" })),
      ),
    ).toThrow("maximum is 50");
  });
});
