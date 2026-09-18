import { breadcrumbLabel, generateBreadcrumbs } from "@/lib/breadcrumbs";

describe("dashboard breadcrumbs", () => {
  it.each([
    ["audit", "Audit"],
    ["catalog", "Catalog"],
    ["fleet", "Fleet Operations"],
    ["network-policies", "Network Policies"],
  ])("labels %s as %s", (segment, expected) => {
    expect(breadcrumbLabel(segment)).toBe(expected);
  });

  it("title-cases an unmapped route slug", () => {
    expect(breadcrumbLabel("cloud-credentials")).toBe("Cloud Credentials");
  });

  it("preserves opaque IDs and resolves known cluster names", () => {
    const id = "11111111-1111-4111-8111-111111111111";
    expect(breadcrumbLabel(id)).toBe(id);
    expect(
      generateBreadcrumbs(`/dashboard/clusters/${id}/network-policies`, {
        [id]: "production-east",
      }).map((crumb) => crumb.label),
    ).toEqual([
      "Dashboard",
      "Clusters",
      "production-east",
      "Network Policies",
    ]);
  });
});
