import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { Box } from "lucide-react";

import {
  activeNavGroupLabel,
  defaultOpenNavGroupLabel,
  globalNavGroups,
  navLabelForHref,
  type NavGroup,
} from "@/components/layout/sidebar-navigation";
import { SETTINGS_NAVIGATION } from "@/components/settings/settings-navigation";

const groups: NavGroup[] = [
  {
    label: "Default",
    defaultOpen: true,
    items: [{ label: "Home", href: "/dashboard", icon: Box, exact: true }],
  },
  {
    label: "Workloads",
    items: [{ label: "Pods", href: "/dashboard/pods", icon: Box }],
  },
];

describe("sidebar accordion group selection", () => {
  it("selects only the group containing the active route", () => {
    expect(activeNavGroupLabel(groups, "/dashboard/pods/ns/pod-a")).toBe(
      "Workloads",
    );
    expect(defaultOpenNavGroupLabel(groups, "/dashboard/pods/ns/pod-a")).toBe(
      "Workloads",
    );
  });

  it("uses the first default group when no route is active", () => {
    expect(defaultOpenNavGroupLabel(groups, "/outside-dashboard")).toBe(
      "Default",
    );
  });

  it("returns no group when neither an active nor default group exists", () => {
    const groupsWithoutDefault = groups.map((group) => ({
      ...group,
      defaultOpen: false,
    }));

    expect(
      defaultOpenNavGroupLabel(groupsWithoutDefault, "/outside-dashboard"),
    ).toBeNull();
  });
});

describe("navLabelForHref", () => {
  it("finds the label for a global nav item by href", () => {
    expect(navLabelForHref("/dashboard/delivery")).toBe("Estate");
    expect(navLabelForHref("/dashboard/security")).toBe("Security");
  });

  it("finds the title for a settings nav item by href", () => {
    expect(navLabelForHref("/dashboard/settings/auth")).toBe("Authentication");
  });

  it("returns undefined for an href not in either registry", () => {
    expect(navLabelForHref("/dashboard/clusters/c-1")).toBeUndefined();
  });
});

describe("nav registry hrefs resolve to real routes", () => {
  const routeTreeSource = readFileSync(
    join(process.cwd(), "src/routeTree.gen.ts"),
    "utf8",
  );
  const hrefs = [
    ...globalNavGroups.flatMap((group) => group.items.map((item) => item.href)),
    ...SETTINGS_NAVIGATION.flatMap((group) =>
      group.items.map((item) => item.href),
    ),
  ];

  it.each([...new Set(hrefs)])("%s resolves to a generated route id", (href) => {
    expect(routeTreeSource).toContain(`id: '${href}/'`);
  });
});
