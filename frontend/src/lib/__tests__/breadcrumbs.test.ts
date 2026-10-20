import { breadcrumbLabel, generateBreadcrumbs } from "@/lib/breadcrumbs";
import { globalNavGroups } from "@/components/layout/sidebar-navigation";
import { SETTINGS_NAVIGATION } from "@/components/settings/settings-navigation";

describe("dashboard breadcrumbs", () => {
  it.each([
    ["audit", "Audit log"],
    ["catalog", "Catalog"],
    ["network-policies", "Network policies"],
  ])("labels %s as %s", (segment, expected) => {
    expect(breadcrumbLabel(segment)).toBe(expected);
  });

  it("sentence-cases an unmapped route slug", () => {
    expect(breadcrumbLabel("cloud-credentials")).toBe("Cloud credentials");
  });

  it("preserves opaque IDs and resolves known cluster names", () => {
    const id = "11111111-1111-4111-8111-111111111111";
    expect(breadcrumbLabel(id)).toBe(id);
    expect(
      generateBreadcrumbs(`/dashboard/clusters/${id}/network-policies`, {
        [id]: "production-east",
      }).map((crumb) => crumb.label),
    ).toEqual([
      "Overview",
      "Clusters",
      "production-east",
      "Network policies",
    ]);
  });
});

describe("breadcrumbs match the nav label registry", () => {
  const registryItems = [
    ...globalNavGroups.flatMap((group) =>
      group.items.map((item) => [item.href, item.label] as const),
    ),
    ...SETTINGS_NAVIGATION.flatMap((group) =>
      group.items.map((item) => [item.href, item.title] as const),
    ),
  ];

  it.each(registryItems)(
    "labels the last breadcrumb for %s as %s",
    (href, label) => {
      const crumbs = generateBreadcrumbs(href);
      expect(crumbs.at(-1)?.label).toBe(label);
    },
  );
});
