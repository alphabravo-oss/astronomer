import { clusterTransitionPath } from "./cluster-navigation-transition";
it.each([
  "deployments/default/same-name",
  "nodes/same-name",
  "pods/default/same-name",
])("drops old object identity for %s", (suffix) => {
  expect(clusterTransitionPath(`/dashboard/clusters/a/${suffix}`, "b")).toBe(
    `/dashboard/clusters/b/${suffix.split("/")[0]}`,
  );
});
it("retains custom collection only when target discovery confirms the served version", () => {
  const source =
    "/dashboard/clusters/a/custom-resources/cert.io/v1/certs/ns/item";
  expect(clusterTransitionPath(source, "b")).toBe("/dashboard/clusters/b");
  expect(clusterTransitionPath(source, "b", ["cert.io/v1/certs"])).toBe(
    "/dashboard/clusters/b/custom-resources/cert.io/v1/certs",
  );
});
it("maps apps/operations and delivery objects to target collection", () => {
  expect(clusterTransitionPath("/dashboard/clusters/a/apps/old", "b")).toBe(
    "/dashboard/clusters/b/apps",
  );
  expect(
    clusterTransitionPath(
      "/dashboard/clusters/a/delivery/deployments/old",
      "b",
    ),
  ).toBe("/dashboard/clusters/b/delivery/deployments");
  expect(clusterTransitionPath("/dashboard/clusters/a/unknown/old", "b")).toBe(
    "/dashboard/clusters/b",
  );
});

it.each([
  "sources",
  "bundles",
  "targets",
  "rollouts",
  "deployments",
  "configuration-templates",
  "override-sets",
  "system-components",
])(
  "preserves the %s collection while discarding its old object ID",
  (collection) => {
    expect(
      clusterTransitionPath(
        `/dashboard/clusters/a/delivery/${collection}/old`,
        "b",
      ),
    ).toBe(`/dashboard/clusters/b/delivery/${collection}`);
  },
);
