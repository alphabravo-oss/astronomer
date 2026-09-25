import { describe, expect, it } from "vitest";
import {
  ownerHref,
  ownedBy,
  relatedPagePath,
  serviceSelectsPod,
} from "./related-resource-model";
import type { ResourceDiscoveryView } from "@/lib/api/resources";
const discovery: ResourceDiscoveryView = {
  clusterId: "c",
  resources: [],
  crds: [
    {
      group: "example.io",
      scope: "Cluster",
      kind: "Deployment",
      plural: "deployments",
      versions: [{ name: "v1", storage: true }],
    },
  ],
  crdContinue: "",
  partial: false,
  errors: {},
};
describe("related resource identity and bounds", () => {
  it("resolves custom kinds by group, served version and discovered scope, never UID", () => {
    expect(
      ownerHref(
        "c",
        "app",
        {
          apiVersion: "example.io/v1",
          kind: "Deployment",
          name: "actual-name",
          uid: "not-a-name",
        },
        discovery,
      ),
    ).toBe(
      "/dashboard/clusters/c/custom-resources/example.io/v1/deployments/actual-name",
    );
    expect(
      ownerHref(
        "c",
        "app",
        { apiVersion: "apps/v1", kind: "Deployment", name: "actual-name" },
        discovery,
      ),
    ).toBeUndefined();
    expect(
      ownerHref(
        "c",
        "app",
        {
          apiVersion: "example.io/v2",
          kind: "Deployment",
          name: "actual-name",
        },
        discovery,
      ),
    ).toBeUndefined();
  });
  it("does not guess owner group when API version is absent", () => {
    expect(
      ownerHref("c", "app", { kind: "Deployment", name: "demo" }, discovery),
    ).toBeUndefined();
  });
  it("requires UID equality when a parent was recreated under the same name", () => {
    expect(
      ownedBy(
        {
          metadata: {
            ownerReferences: [{ name: "demo", kind: "Job", uid: "old" }],
          },
        },
        { name: "demo", kind: "Job", uid: "new" },
      ),
    ).toBe(false);
  });
  it("requires a nonempty Service selector", () => {
    expect(
      serviceSelectsPod(
        { spec: {} },
        { metadata: { labels: { app: "demo" } } },
      ),
    ).toBe(false);
    expect(
      serviceSelectsPod(
        { spec: { selector: { app: "demo" } } },
        { metadata: { labels: { app: "demo" } } },
      ),
    ).toBe(true);
  });
  it("caps requests and escapes opaque continuation tokens", () => {
    const path = relatedPagePath("pods", "app", "opaque+/=&");
    const query = new URLSearchParams(path.split("?")[1]);
    expect(query.get("limit")).toBe("50");
    expect(query.get("continue")).toBe("opaque+/=&");
  });
});
