import {
  podInvestigationHref,
  safeWorkloadOrigin,
} from "./resource-navigation-context";
it("retains the originating workload Pods tab and scope without action state", () => {
  const origin =
    "/dashboard/clusters/a/deployments/default/web?tab=workload-pods&namespaces=default&install=bad";
  expect(safeWorkloadOrigin(origin, "a")).toBe(
    "/dashboard/clusters/a/deployments/default/web?namespaces=default&tab=workload-pods",
  );
  const href = podInvestigationHref(
    "/dashboard/clusters/a/pods/default/web-1",
    origin,
    "?namespaces=default&tab=workload-pods&install=bad",
  );
  expect(new URLSearchParams(href.split("?")[1]).get("namespaces")).toBe(
    "default",
  );
  expect(new URLSearchParams(href.split("?")[1]).has("install")).toBe(false);
});
it.each([
  "https://evil.example",
  "//evil.example",
  "/dashboard/clusters/b/deployments/ns/x",
  "/dashboard/clusters/a/deployments/../x",
  "/dashboard/clusters/a/pods/ns/x",
])("rejects unsafe/unrelated origin %s", (origin) => {
  expect(safeWorkloadOrigin(origin, "a")).toBeUndefined();
});
